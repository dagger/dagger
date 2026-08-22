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
	"github.com/vito/go-sse/sse"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestTelemetryContextUsesClientLifetime(t *testing.T) {
	t.Parallel()

	internalCtx, cancelInternal := context.WithCancelCause(context.Background())
	client := &Client{internalCtx: internalCtx}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	telemetryCtx, cancelTelemetry := client.telemetryContext(requestCtx)
	defer cancelTelemetry(context.Canceled)

	cancelRequest()
	select {
	case <-telemetryCtx.Done():
		t.Fatal("request cancellation interrupted client telemetry drain")
	case <-time.After(10 * time.Millisecond):
	}

	clientClosed := errors.New("client initialization failed")
	cancelInternal(clientClosed)
	select {
	case <-telemetryCtx.Done():
		require.ErrorIs(t, context.Cause(telemetryCtx), clientClosed)
	case <-time.After(time.Second):
		t.Fatal("client lifetime cancellation did not stop telemetry")
	}
}

func TestOTLPConsumerStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reader, writer := io.Pipe()
	defer writer.Close()
	body := &observedReadCloser{
		ReadCloser:  reader,
		readStarted: make(chan struct{}),
	}
	defer body.Close()

	httpClient := &httpClient{
		inner: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{enginetel.LiveContentType},
					},
					Body:    body,
					Request: req,
				}, nil
			}),
		},
	}
	telemetry := new(errgroup.Group)
	consumer := &otlpConsumer{
		httpClient: httpClient,
		path:       "/v1/traces",
		eg:         telemetry,
	}

	require.NoError(t, consumer.Consume(ctx, func([]byte, liveTelemetryEncoding) error {
		return errors.New("unexpected telemetry event")
	}))

	select {
	case <-body.readStarted:
	case <-time.After(time.Second):
		t.Fatal("telemetry consumer did not start reading")
	}

	cancel()
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- telemetry.Wait()
	}()

	select {
	case err := <-waitDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("telemetry consumer did not stop after context cancellation")
	}
}

func TestOTLPConsumerReconnectsFromLastCursor(t *testing.T) {
	t.Parallel()

	var requests int
	httpClient := &httpClient{
		inner: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				var stream bytes.Buffer
				switch requests {
				case 1:
					if cursor := req.Header.Get(enginetel.LiveCursorHeader); cursor != "" {
						return nil, fmt.Errorf("initial cursor = %q", cursor)
					}
					if err := enginetel.WriteLiveFrame(&stream, 7, []byte("batch one")); err != nil {
						return nil, err
					}
				case 2:
					if cursor := req.Header.Get(enginetel.LiveCursorHeader); cursor != "7" {
						return nil, fmt.Errorf("resume cursor = %q, want 7", cursor)
					}
					if cursor := req.Header.Get(enginetel.LegacyLiveCursorHeader); cursor != "7" {
						return nil, fmt.Errorf("legacy resume cursor = %q, want 7", cursor)
					}
					if cursor := req.Header.Get("Last-Event-ID"); cursor != "7" {
						return nil, fmt.Errorf("standard SSE resume cursor = %q, want 7", cursor)
					}
					if err := enginetel.WriteLiveFrame(&stream, 9, []byte("batch two")); err != nil {
						return nil, err
					}
					if err := enginetel.WriteLiveTerminal(&stream, 9); err != nil {
						return nil, err
					}
				default:
					return nil, fmt.Errorf("unexpected request %d", requests)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{enginetel.LiveContentType},
					},
					Body:    io.NopCloser(bytes.NewReader(stream.Bytes())),
					Request: req,
				}, nil
			}),
		},
	}

	telemetryGroup := new(errgroup.Group)
	consumer := &otlpConsumer{
		httpClient: httpClient,
		path:       "/v1/traces",
		eg:         telemetryGroup,
	}
	var batches [][]byte
	require.NoError(t, consumer.Consume(context.Background(), func(payload []byte, _ liveTelemetryEncoding) error {
		batches = append(batches, bytes.Clone(payload))
		return nil
	}))

	waitDone := make(chan error, 1)
	go func() { waitDone <- telemetryGroup.Wait() }()
	select {
	case err := <-waitDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("telemetry consumer did not finish after terminal frame")
	}
	require.Equal(t, [][]byte{[]byte("batch one"), []byte("batch two")}, batches)
	require.Equal(t, 2, requests)
}

func TestOTLPConsumerDecodesLegacySSEProtoJSON(t *testing.T) {
	t.Parallel()

	rawBody := []byte{0, 1, 2, 0xff}
	batch := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					Body: &commonv1.AnyValue{Value: &commonv1.AnyValue_BytesValue{BytesValue: rawBody}},
				}},
			}},
		}},
	}
	payload, err := protojson.Marshal(batch)
	require.NoError(t, err)
	var stream bytes.Buffer
	require.NoError(t, (sse.Event{Name: "subscribed"}).Write(&stream))
	require.NoError(t, (sse.Event{Name: "logs", ID: "4", Data: payload}).Write(&stream))

	httpClient := &httpClient{
		inner: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				accept := req.Header.Get("Accept")
				if !strings.Contains(accept, enginetel.LiveContentType) || !strings.Contains(accept, enginetel.LegacyLiveContentType) {
					return nil, fmt.Errorf("Accept = %q", accept)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{enginetel.LegacyLiveContentType + "; charset=utf-8"},
					},
					Body:    io.NopCloser(bytes.NewReader(stream.Bytes())),
					Request: req,
				}, nil
			}),
		},
	}

	telemetryGroup := new(errgroup.Group)
	consumer := &otlpConsumer{
		httpClient: httpClient,
		path:       "/v1/logs",
		eg:         telemetryGroup,
	}
	var got collogspb.ExportLogsServiceRequest
	var gotEncoding liveTelemetryEncoding
	var decodeErr error
	require.NoError(t, consumer.Consume(context.Background(), func(data []byte, encoding liveTelemetryEncoding) error {
		gotEncoding = encoding
		decodeErr = unmarshalLiveTelemetry(data, encoding, &got)
		return decodeErr
	}))
	require.NoError(t, telemetryGroup.Wait())
	require.NoError(t, decodeErr)
	require.Equal(t, liveTelemetryProtoJSON, gotEncoding)
	require.Equal(t, rawBody, got.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Body.GetBytesValue())
}

func TestOTLPConsumerDoesNotReconnectStreamErrors(t *testing.T) {
	t.Parallel()

	var requests int
	httpClient := &httpClient{
		inner: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				var stream bytes.Buffer
				if err := enginetel.WriteLiveError(&stream, 0, errors.New("oversized telemetry row")); err != nil {
					return nil, err
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{enginetel.LiveContentType},
					},
					Body:    io.NopCloser(bytes.NewReader(stream.Bytes())),
					Request: req,
				}, nil
			}),
		},
	}

	telemetryGroup := new(errgroup.Group)
	consumer := &otlpConsumer{
		httpClient: httpClient,
		path:       "/v1/traces",
		eg:         telemetryGroup,
	}
	require.NoError(t, consumer.Consume(context.Background(), func([]byte, liveTelemetryEncoding) error {
		return errors.New("unexpected telemetry event")
	}))

	waitDone := make(chan error, 1)
	go func() { waitDone <- telemetryGroup.Wait() }()
	select {
	case err := <-waitDone:
		require.ErrorIs(t, err, enginetel.ErrLiveStream)
	case <-time.After(time.Second):
		t.Fatal("telemetry consumer retried a non-reconnectable stream error")
	}
	require.Equal(t, 1, requests)
}

func TestClientMetadataUsesExplicitModuleInsteadOfWorkspaceModules(t *testing.T) {
	t.Parallel()

	client := &Client{
		Params: Params{
			ID:                   "client",
			SessionID:            "session",
			SecretToken:          "secret",
			Module:               "./explicit",
			LoadWorkspaceModules: true,
		},
	}

	md := client.clientMetadata()

	require.False(t, md.LoadWorkspaceModules)
	require.Equal(t, []engine.ExtraModule{{
		Ref:        "./explicit",
		Entrypoint: true,
	}}, md.ExtraModules)
}

func TestClientMetadataForwardsWorkspaceModuleScopeOnlyWithWorkspaceModules(t *testing.T) {
	t.Parallel()

	client := &Client{
		Params: Params{
			ID:                   "client",
			SessionID:            "session",
			SecretToken:          "secret",
			LoadWorkspaceModules: true,
			WorkspaceModuleScope: "good-mod",
		},
	}

	md := client.clientMetadata()
	require.True(t, md.LoadWorkspaceModules)
	require.Equal(t, "good-mod", md.WorkspaceModuleScope)

	// With an explicit -m module there are no pending workspace modules to
	// narrow, so the scope must not travel.
	client.Params.Module = "./explicit"
	md = client.clientMetadata()
	require.False(t, md.LoadWorkspaceModules)
	require.Empty(t, md.WorkspaceModuleScope)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type observedReadCloser struct {
	io.ReadCloser
	readStarted chan struct{}
	readOnce    sync.Once
}

func (body *observedReadCloser) Read(p []byte) (int, error) {
	body.readOnce.Do(func() {
		close(body.readStarted)
	})
	return body.ReadCloser.Read(p)
}
