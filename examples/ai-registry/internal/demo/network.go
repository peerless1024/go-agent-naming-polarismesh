/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package demo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	modelpb "github.com/polarismesh/polaris-go/pkg/model/pb"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	apiskill "github.com/polarismesh/specification/source/go/api/v1/skill_manage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TCPGate is a byte-for-byte proxy; it never generates Polaris responses.
// Only the dedicated fault-injection consumer connects through it.
type TCPGate struct {
	listener net.Listener
	upstream string
	mu       sync.Mutex
	paused   bool
	closed   bool
	conns    map[net.Conn]bool
	wg       sync.WaitGroup
}

// NewTCPGate starts a loopback-only proxy to the real server.
func NewTCPGate(upstream string) (*TCPGate, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	g := &TCPGate{listener: listener, upstream: upstream, conns: make(map[net.Conn]bool)}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			g.mu.Lock()
			if g.paused || g.closed {
				g.mu.Unlock()
				_ = client.Close()
				continue
			}
			g.conns[client] = true
			g.wg.Add(1)
			g.mu.Unlock()
			go g.forward(client)
		}
	}()
	return g, nil
}

func (g *TCPGate) forward(client net.Conn) {
	defer g.wg.Done()
	defer func() {
		_ = client.Close()
		g.mu.Lock()
		delete(g.conns, client)
		g.mu.Unlock()
	}()
	upstream, err := net.DialTimeout("tcp", g.upstream, 3*time.Second)
	if err != nil {
		return
	}
	defer func() {
		_ = upstream.Close()
		g.mu.Lock()
		delete(g.conns, upstream)
		g.mu.Unlock()
	}()
	g.mu.Lock()
	if g.paused || g.closed {
		g.mu.Unlock()
		return
	}
	g.conns[upstream] = true
	g.mu.Unlock()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
		_ = client.Close()
		close(done)
	}()
	_, _ = io.Copy(client, upstream)
	_ = upstream.Close()
	_ = client.Close()
	<-done
}

// Address is the fault consumer's ephemeral gRPC endpoint.
func (g *TCPGate) Address() string { return g.listener.Addr().String() }

// Pause drops existing connections and rejects new ones without remote changes.
func (g *TCPGate) Pause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paused = true
	for conn := range g.conns {
		_ = conn.Close()
	}
}

// Resume enables transparent forwarding to the same real deployment.
func (g *TCPGate) Resume() {
	g.mu.Lock()
	g.paused = false
	g.mu.Unlock()
}

// Close waits for all proxy copies and listeners to exit.
func (g *TCPGate) Close() {
	g.mu.Lock()
	g.closed = true
	_ = g.listener.Close()
	for conn := range g.conns {
		_ = conn.Close()
	}
	g.mu.Unlock()
	g.wg.Wait()
}

// LiveLister implements the adapter's public injection interface using the real
// Polaris Discover stream, so SDK stale cache cannot mask a network fault.
type LiveLister struct {
	conn     *grpc.ClientConn
	client   apiservice.PolarisGRPCClient
	token    string
	Failures atomic.Int64
}

// ServiceProjection records only non-sensitive protocol capabilities.
type ServiceProjection struct {
	Code                                         uint32
	Namespace, Service                           string
	ListServiceType, InstanceServiceType         string
	ListFound, ListRevision, ListServiceRevision bool
	InstanceFound, InstanceServiceRevision       bool
	ListRunMetadataMatches                       bool
	InstanceRunMetadataMatches                   bool
	ListExtendedMetadataCount                    int
	InstanceExtendedMetadataCount                int
}

// Inspect performs read-only protocol and Console diagnostics without printing credentials.
func Inspect(ctx context.Context, o Options) error {
	cfg, err := LoadConfiguration(o, false)
	if err != nil {
		return err
	}
	if o.Service != "" {
		probeCtx, cancel := context.WithTimeout(ctx, o.Timeout)
		projection, err := inspectServiceProjection(probeCtx, o.Discover,
			cfg.GetGlobal().GetServerConnector().GetToken(), o.Namespace, o.Service, o.RunID)
		cancel()
		if err != nil {
			return err
		}
		if err := WriteJSON(filepath.Join(o.RunDir, "service-projection.json"), projection); err != nil {
			return err
		}
	}
	if o.SkillName == "" {
		return nil
	}
	console := NewConsole(o, cfg)
	defer console.Close()
	envelope, err := console.Request(ctx, http.MethodGet, "/skill/v1/skills?"+url.Values{
		"namespace": {o.Namespace}, "name": {o.SkillName}, "version": {o.SkillVersion},
	}.Encode(), nil)
	if err != nil {
		return err
	}
	lister, err := NewLiveLister(o.SkillAddress, cfg.GetSkill().GetToken())
	if err != nil {
		return err
	}
	defer func() { _ = lister.Close() }()
	probeCtx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if lister.token != "" {
		probeCtx = metadata.AppendToOutgoingContext(probeCtx, "X-Polaris-Token", lister.token)
	}
	response, err := apiskill.NewPolarisSkillClient(lister.conn).GetSkill(probeCtx, &apiskill.GetSkillRequest{
		Namespace: o.Namespace, Name: o.SkillName, Version: o.SkillVersion,
	})
	if err != nil {
		return err
	}
	if response == nil || envelope.Version == nil {
		return errors.New("skill diagnostic returned an incomplete response")
	}
	return WriteJSON(filepath.Join(o.RunDir, "skill-projection.json"), struct {
		Namespace, Name, RequestedVersion                          string
		ConsoleCode, ClientCode                                    uint32
		ConsoleVersion, ConsoleStatus, ClientVersion, ClientStatus string
	}{o.Namespace, o.SkillName, o.SkillVersion, envelope.Code, response.Code,
		envelope.Version.Version, envelope.Version.Status, response.GetVersion().GetVersion(), response.GetVersion().GetStatus()})
}

func inspectServiceProjection(ctx context.Context, address, token, namespace, service, runID string) (*ServiceProjection, error) {
	lister, err := NewLiveLister(address, token)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lister.Close() }()
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "X-Polaris-Token", token)
	}
	projection := &ServiceProjection{Namespace: namespace, Service: service}
	listResponse, err := discoverOnce(ctx, lister.client, apiservice.DiscoverRequest_SERVICES, namespace, service)
	if err != nil {
		return nil, err
	}
	projection.Code = listResponse.GetCode().GetValue()
	projection.ListRevision = listResponse.GetService().GetRevision().GetValue() != ""
	for _, item := range listResponse.GetServices() {
		if item.GetNamespace().GetValue() == namespace && item.GetName().GetValue() == service {
			projection.ListFound = true
			projection.ListServiceType = item.GetServiceType().String()
			projection.ListServiceRevision = item.GetRevision().GetValue() != ""
			projection.ListExtendedMetadataCount = len(item.GetExtendedMetadata())
			projection.ListRunMetadataMatches = item.GetMetadata()[RunLabel] == runID
			break
		}
	}
	instanceResponse, err := discoverOnce(ctx, lister.client, apiservice.DiscoverRequest_INSTANCE, namespace, service)
	if err != nil {
		return nil, err
	}
	item := instanceResponse.GetService()
	projection.InstanceFound = item.GetNamespace().GetValue() == namespace &&
		item.GetName().GetValue() == service
	projection.InstanceServiceType = item.GetServiceType().String()
	projection.InstanceServiceRevision = item.GetRevision().GetValue() != ""
	projection.InstanceExtendedMetadataCount = len(item.GetExtendedMetadata())
	projection.InstanceRunMetadataMatches = item.GetMetadata()[RunLabel] == runID
	return projection, nil
}

func discoverOnce(
	ctx context.Context, client apiservice.PolarisGRPCClient, requestType apiservice.DiscoverRequest_DiscoverRequestType,
	namespace, service string,
) (*apiservice.DiscoverResponse, error) {
	stream, err := client.Discover(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&apiservice.DiscoverRequest{Type: requestType,
		Service: &apiservice.Service{Namespace: wrapperspb.String(namespace), Name: wrapperspb.String(service)},
	}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("real Polaris returned a nil %s response", requestType)
	}
	return response, nil
}

// NewLiveLister creates a real gRPC client to the transparent fault proxy.
func NewLiveLister(address, token string) (*LiveLister, error) {
	conn, err := grpc.NewClient("passthrough:///"+address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}))
	if err != nil {
		return nil, err
	}
	return &LiveLister{conn: conn, client: apiservice.NewPolarisGRPCClient(conn), token: token}, nil
}

// Close releases the real gRPC client connection.
func (l *LiveLister) Close() error { return l.conn.Close() }

// GetInstances sends an INSTANCE request on a real bidirectional Discover stream.
func (l *LiveLister) GetInstances(req *api.GetInstancesRequest) (*model.InstancesResponse, error) {
	timeout := 2 * time.Second
	if req.Timeout != nil && *req.Timeout > 0 {
		timeout = *req.Timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if l.token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "X-Polaris-Token", l.token)
	}
	stream, err := l.client.Discover(ctx)
	if err == nil {
		err = stream.Send(&apiservice.DiscoverRequest{Type: apiservice.DiscoverRequest_INSTANCE,
			Service: &apiservice.Service{Namespace: wrapperspb.String(req.Namespace), Name: wrapperspb.String(req.Service)}})
	}
	var response *apiservice.DiscoverResponse
	if err == nil {
		response, err = stream.Recv()
	}
	if err != nil {
		l.Failures.Add(1)
		return nil, model.NewSDKError(model.ErrCodeNetworkError, err, "real Polaris discovery transport failed")
	}
	if response.GetCode().GetValue() != 200000 {
		return nil, model.NewSDKError(model.ErrCodeServiceNotFound, nil, "real Polaris discovery code=%d", response.GetCode().GetValue())
	}
	result := &model.InstancesResponse{Revision: response.GetService().GetRevision().GetValue()}
	key := &model.ServiceKey{Namespace: req.Namespace, Service: req.Service}
	for _, instance := range response.Instances {
		if instance == nil {
			return nil, fmt.Errorf("real Polaris response contains a nil instance")
		}
		result.Instances = append(result.Instances, modelpb.NewInstanceInProto(instance, key, nil))
	}
	return result, nil
}
