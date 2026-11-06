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
	"io"
	"net"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestTCPGateForwardsDropsAndRecovers(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn, 2)
	completed := make(chan struct{})
	defer func() {
		_ = listener.Close()
		<-completed
	}()
	go func() {
		defer close(completed)
		for index := 0; index < 2; index++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
			_, _ = io.Copy(conn, conn)
			_ = conn.Close()
		}
	}()
	gate, err := NewTCPGate(listener.Addr().String())
	require.NoError(t, err)
	defer gate.Close()
	conn, err := net.DialTimeout("tcp", gate.Address(), time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(time.Second)))
	_, err = conn.Write([]byte("transparent"))
	require.NoError(t, err)
	data := make([]byte, len("transparent"))
	_, err = io.ReadFull(conn, data)
	require.NoError(t, err)
	require.Equal(t, "transparent", string(data))
	upstream := <-accepted
	defer func() { _ = upstream.Close() }()
	gate.Pause()
	_, err = conn.Read(data)
	require.Error(t, err)
	gate.Resume()
	recovered, err := net.DialTimeout("tcp", gate.Address(), time.Second)
	require.NoError(t, err)
	defer func() { _ = recovered.Close() }()
	require.NoError(t, recovered.SetDeadline(time.Now().Add(time.Second)))
	_, err = recovered.Write([]byte("reconnected"))
	require.NoError(t, err)
	data = make([]byte, len("reconnected"))
	_, err = io.ReadFull(recovered, data)
	require.NoError(t, err)
	require.Equal(t, "reconnected", string(data))
	upstream = <-accepted
	defer func() { _ = upstream.Close() }()
	gate.Close()
	gate.Close()
}

type discoverServer struct {
	apiservice.UnimplementedPolarisGRPCServer
	token chan string
}

func (s *discoverServer) Discover(stream apiservice.PolarisGRPC_DiscoverServer) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	s.token <- firstValue(md.Get("x-polaris-token"))
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	return stream.Send(&apiservice.DiscoverResponse{Code: wrapperspb.UInt32(200000), Type: apiservice.DiscoverResponse_INSTANCE,
		Service: &apiservice.Service{Namespace: wrapperspb.String(req.GetService().GetNamespace().GetValue()),
			Name: wrapperspb.String(req.GetService().GetName().GetValue()), Revision: wrapperspb.String("real-wire-revision")},
		Instances: []*apiservice.Instance{{Id: wrapperspb.String("node"), Host: wrapperspb.String("127.0.0.1"),
			Port: wrapperspb.UInt32(18080), Healthy: wrapperspb.Bool(true)}},
	})
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func TestLiveListerUsesPolarisTokenAndRealWireShape(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	defer server.Stop()
	discovery := &discoverServer{token: make(chan string, 1)}
	apiservice.RegisterPolarisGRPCServer(server, discovery)
	go func() { _ = server.Serve(listener) }()
	lister, err := NewLiveLister(listener.Addr().String(), "synthetic-test-token")
	require.NoError(t, err)
	defer func() { require.NoError(t, lister.Close()) }()
	req := &api.GetInstancesRequest{GetInstancesRequest: model.GetInstancesRequest{Namespace: "test", Service: "agent"}}
	response, err := lister.GetInstances(req)
	require.NoError(t, err)
	require.Equal(t, "synthetic-test-token", <-discovery.token)
	require.Equal(t, "real-wire-revision", response.Revision)
	require.Len(t, response.GetInstances(), 1)
	require.True(t, response.GetInstances()[0].IsHealthy())
	require.Zero(t, lister.Failures.Load())
}

type projectionServer struct {
	apiservice.UnimplementedPolarisGRPCServer
	token chan string
}

func (s *projectionServer) Discover(stream apiservice.PolarisGRPC_DiscoverServer) error {
	request, err := stream.Recv()
	if err != nil {
		return err
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	select {
	case s.token <- firstValue(md.Get("x-polaris-token")):
	default:
	}
	service := &apiservice.Service{
		Namespace:        wrapperspb.String(request.GetService().GetNamespace().GetValue()),
		Name:             wrapperspb.String(request.GetService().GetName().GetValue()),
		Metadata:         map[string]string{RunLabel: "projection-run"},
		Revision:         wrapperspb.String("service-revision"),
		ServiceType:      apiservice.ServiceType_SERVICE_TYPE_AI_AGENT,
		ExtendedMetadata: []*apiservice.ExtendedMetadata{{Type: apiservice.ExtendedMetadata_EXTENDED_METADATA_SKILL}},
	}
	response := &apiservice.DiscoverResponse{
		Code:    wrapperspb.UInt32(200000),
		Service: service,
	}
	if request.GetType() == apiservice.DiscoverRequest_SERVICES {
		response.Services = []*apiservice.Service{service}
	}
	return stream.Send(response)
}

func TestInspectServiceProjectionReadsListAndInstanceCapabilities(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	defer server.Stop()
	discovery := &projectionServer{token: make(chan string, 2)}
	apiservice.RegisterPolarisGRPCServer(server, discovery)
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	projection, err := inspectServiceProjection(
		ctx, listener.Addr().String(), "synthetic-test-token", "test", "agent", "projection-run",
	)
	require.NoError(t, err)
	require.Equal(t, "synthetic-test-token", <-discovery.token)
	require.Equal(t, uint32(200000), projection.Code)
	require.True(t, projection.ListFound)
	require.True(t, projection.ListRevision)
	require.True(t, projection.ListServiceRevision)
	require.True(t, projection.ListRunMetadataMatches)
	require.Equal(t, "SERVICE_TYPE_AI_AGENT", projection.ListServiceType)
	require.Equal(t, 1, projection.ListExtendedMetadataCount)
	require.True(t, projection.InstanceFound)
	require.True(t, projection.InstanceServiceRevision)
	require.True(t, projection.InstanceRunMetadataMatches)
	require.Equal(t, "SERVICE_TYPE_AI_AGENT", projection.InstanceServiceType)
	require.Equal(t, 1, projection.InstanceExtendedMetadataCount)
}
