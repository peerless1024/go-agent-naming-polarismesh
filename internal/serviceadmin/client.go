/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package serviceadmin provides the Console HTTP transport used for AI Agent service management.
package serviceadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	serviceRequestPath       = "/naming/v1/services"
	serviceDeleteRequestPath = "/naming/v1/services/delete"
	headerToken              = "X-Polaris-Token"
	headerRequestID          = "request-id"
	requestIDPrefix          = 61
	maxErrorBodyBytes        = 1024
)

// Client 是北极星 Console HTTP 客户端，仅用于 AI Agent 服务写入。
type Client struct {
	addresses     []string
	token         string
	timeout       time.Duration
	http          *http.Client
	next          uint32
	stateMutex    sync.Mutex
	serviceStates map[string]serviceWriteState
	writeLocks    sync.Map
}

type jsonRequestResult struct {
	body      []byte
	status    int
	uncertain bool
	err       error
}

// New 创建 Console HTTP 客户端。
func New(cfg ServiceWriterConfig) *Client {
	return &Client{
		addresses:     append([]string(nil), cfg.GetAddresses()...),
		token:         cfg.GetToken(),
		timeout:       cfg.GetTimeout(),
		serviceStates: make(map[string]serviceWriteState),
		http: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Close releases only the optional Console client's idle connections.
func (c *Client) Close() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

func (c *Client) doJSON(
	ctx context.Context, method, path string, query url.Values, payload interface{},
) ([]byte, int, error) {
	result := c.doJSONWithAttempts(ctx, method, path, query, payload)
	return result.body, result.status, result.err
}

func (c *Client) doJSONWithAttempts(
	ctx context.Context, method, path string, query url.Values, payload interface{},
) jsonRequestResult {
	if c == nil || len(c.addresses) == 0 {
		return jsonRequestResult{err: model.NewSDKError(
			model.ErrCodeServiceWriteUnsupported, nil,
			"serviceWriter.addresses is not configured",
		)}
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return jsonRequestResult{err: model.NewSDKError(
				model.ErrCodeServiceWriteError, err,
				"fail to encode Console request",
			)}
		}
	}
	start := int((atomic.AddUint32(&c.next, 1) - 1) % uint32(len(c.addresses)))
	var lastErr error
	uncertain := false
	for offset := 0; offset < len(c.addresses); offset++ {
		address := c.addresses[(start+offset)%len(c.addresses)]
		requestURL := strings.TrimRight(normalizeAddress(address), "/") + path
		if len(query) > 0 {
			requestURL += "?" + query.Encode()
		}
		request, reqErr := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
		if reqErr != nil {
			return jsonRequestResult{uncertain: uncertain, err: model.NewSDKError(
				model.ErrCodeServiceWriteError, reqErr,
				"fail to create Console HTTP request for %s", requestURL)}
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(headerRequestID, fmt.Sprintf("%d%d", requestIDPrefix, uuid.New().ID()))
		if c.token != "" {
			request.Header.Set(headerToken, c.token)
		}
		response, reqErr := c.http.Do(request)
		if reqErr != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			uncertain = true
			lastErr = reqErr
			continue
		}
		var responseBuffer bytes.Buffer
		_, readErr := responseBuffer.ReadFrom(io.LimitReader(response.Body, 8*1024*1024))
		closeErr := response.Body.Close()
		if readErr != nil {
			uncertain = true
			lastErr = readErr
			continue
		}
		if closeErr != nil {
			uncertain = true
			lastErr = closeErr
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			detail := fmt.Sprintf("Console HTTP %s %s returned status %d: %s",
				method, path, response.StatusCode, errorBodyText(responseBuffer.Bytes()))
			if uncertain {
				detail += "; an earlier address attempt had an uncertain outcome"
			}
			return jsonRequestResult{
				body:      responseBuffer.Bytes(),
				status:    response.StatusCode,
				uncertain: uncertain,
				err:       model.NewSDKError(model.ErrCodeServiceWriteError, nil, "%s", detail),
			}
		}
		return jsonRequestResult{
			body:      responseBuffer.Bytes(),
			status:    response.StatusCode,
			uncertain: uncertain,
		}
	}
	detail := "all Console serviceWriter addresses are unavailable"
	if uncertain {
		detail += "; an earlier address attempt had an uncertain outcome"
	}
	return jsonRequestResult{
		uncertain: uncertain,
		err: model.NewSDKError(model.ErrCodeServiceWriteError, lastErr,
			"%s", detail),
	}
}

func errorBodyText(body []byte) string {
	truncated := len(body) > maxErrorBodyBytes
	if truncated {
		body = body[:maxErrorBodyBytes]
	}
	text := strings.TrimSpace(string(body))
	if truncated {
		return text + "..."
	}
	return text
}

func normalizeAddress(address string) string {
	address = strings.TrimSpace(address)
	if strings.Contains(address, "://") {
		return address
	}
	return "http://" + address
}

func (c *Client) context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.timeout)
}

func (c *Client) contextWithTimeout(timeout *time.Duration) (context.Context, context.CancelFunc) {
	value := c.timeout
	if timeout != nil && *timeout > 0 && *timeout < value {
		value = *timeout
	}
	return context.WithTimeout(context.Background(), value)
}

func (c *Client) lockServiceWrite(ctx context.Context, namespace, service string) (func(), error) {
	value, _ := c.writeLocks.LoadOrStore(serviceStateKey(namespace, service), make(chan struct{}, 1))
	semaphore := value.(chan struct{})
	select {
	case semaphore <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-semaphore
			return nil, model.NewSDKError(model.ErrCodeServiceWriteError, err,
				"service write for %s/%s timed out while waiting for another write", namespace, service)
		}
		return func() { <-semaphore }, nil
	case <-ctx.Done():
		return nil, model.NewSDKError(model.ErrCodeServiceWriteError, ctx.Err(),
			"service write for %s/%s timed out while waiting for another write", namespace, service)
	}
}
