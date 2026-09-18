package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestClientCloseAfterLostTelemetryTerminal(t *testing.T) {
	internalCtx, cancelInternal := context.WithCancelCause(context.Background())
	defer cancelInternal(context.Canceled)
	closeCtx, closeRequests := context.WithCancelCause(context.Background())
	shutdown := make(chan struct{})
	reconnected := make(chan struct{})
	var once sync.Once
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() {
		select {
		case <-shutdown:
		case <-internalCtx.Done():
		}
		writer.Close()
	}()
	requests := 0
	hc := &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == engine.ShutdownEndpoint {
			close(shutdown)
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		requests++
		if requests == 1 {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {enginetel.LiveContentType}}, Body: reader, Request: req}, nil
		}
		once.Do(func() { close(reconnected) })
		return &http.Response{StatusCode: http.StatusGone, Body: io.NopCloser(strings.NewReader("nested client transport is closed")), Request: req}, nil
	})}}
	c := &Client{internalCtx: internalCtx, internalCancel: cancelInternal, closeCtx: closeCtx,
		closeRequests: closeRequests, httpClient: hc, telemetry: new(errgroup.Group), eg: new(errgroup.Group)}
	consumer := &otlpConsumer{httpClient: hc, path: "/v1/traces", eg: c.telemetry}
	ctx, cancelTelemetry := c.telemetryContext(context.Background())
	defer cancelTelemetry(context.Canceled)
	require.NoError(t, consumer.Consume(ctx, func([]byte, liveTelemetryEncoding) error { return nil }))
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	select {
	case <-reconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("no reconnect attempt")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, errPermanentTelemetryConnection)
		require.Equal(t, 2, requests)
	case <-time.After(1500 * time.Millisecond):
		t.Error("Client.Close remains blocked after successful shutdown and permanent reconnect failure")
		cancelInternal(context.Canceled)
		<-done
	}
}

func TestOTLPConsumerRetriesInitialConnection(t *testing.T) {
	for _, status := range []int{0, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var stream bytes.Buffer
			require.NoError(t, enginetel.WriteLiveTerminal(&stream, 0))
			requests := 0
			hc := &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					if status == 0 {
						return nil, errors.New("temporary transport failure")
					}
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("temporarily unavailable")), Request: req}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {enginetel.LiveContentType}}, Body: io.NopCloser(&stream), Request: req}, nil
			})}}
			group := new(errgroup.Group)
			consumer := &otlpConsumer{httpClient: hc, path: "/v1/traces", eg: group}
			require.NoError(t, consumer.Consume(ctx, func([]byte, liveTelemetryEncoding) error { return nil }))
			require.NoError(t, group.Wait())
			require.Equal(t, 2, requests)
		})
	}
}

func TestOTLPConsumerRejectsPermanentConnectionErrors(t *testing.T) {
	for _, status := range []int{200, 400, 401, 404, 410} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			requests := 0
			hc := &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("not a telemetry stream")), Request: req}, nil
			})}}
			consumer := &otlpConsumer{httpClient: hc, path: "/v1/traces", eg: new(errgroup.Group)}
			err := consumer.Consume(ctx, func([]byte, liveTelemetryEncoding) error { return nil })
			require.ErrorIs(t, err, errPermanentTelemetryConnection)
			require.Equal(t, 1, requests)
		})
	}
}

func TestClientCloseBoundsTelemetryDrain(t *testing.T) {
	t.Setenv(shutdownTimeoutEnvName, "50ms")
	internalCtx, cancelInternal := context.WithCancelCause(t.Context())
	defer cancelInternal(context.Canceled)
	closeCtx, closeRequests := context.WithCancelCause(t.Context())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	hc := &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == engine.ShutdownEndpoint {
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {enginetel.LiveContentType}}, Body: reader, Request: req}, nil
	})}}
	c := &Client{internalCtx: internalCtx, internalCancel: cancelInternal, closeCtx: closeCtx,
		closeRequests: closeRequests, httpClient: hc, telemetry: new(errgroup.Group), eg: new(errgroup.Group)}
	consumer := &otlpConsumer{httpClient: hc, path: "/v1/traces", eg: c.telemetry}
	ctx, cancelTelemetry := c.telemetryContext(t.Context())
	defer cancelTelemetry(context.Canceled)
	require.NoError(t, consumer.Consume(ctx, func([]byte, liveTelemetryEncoding) error { return nil }))
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(3 * time.Second):
		cancelInternal(context.Canceled)
		<-done
		t.Fatal("telemetry drain did not honor shutdown timeout")
	}
}
