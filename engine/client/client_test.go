package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/prompt"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/stretchr/testify/require"
	"github.com/vito/go-sse/sse"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

type nestedPromptHandler struct {
	boolPrompts int
	forms       int
}

func (h *nestedPromptHandler) HandlePrompt(_ context.Context, _, _ string, dest any) error {
	h.boolPrompts++
	*dest.(*bool) = false
	return nil
}

func (h *nestedPromptHandler) HandleForm(_ context.Context, _ *huh.Form) error {
	h.forms++
	return errors.New("user canceled selection")
}

func TestNestedClientServesLocalPromptsBeforeInit(t *testing.T) {
	// Exercise Connect and the HTTP-to-gRPC reverse connection, not just the
	// Prompt service in isolation. Init represents startup workspace capture.
	for _, failInit := range []bool{false, true} {
		t.Run(fmt.Sprintf("init failure=%t", failInit), func(t *testing.T) {
			handler := &nestedPromptHandler{}
			connections := make(chan *grpc.ClientConn, 1)
			initDone := make(chan struct{})
			var promptConn *grpc.ClientConn
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case engine.SessionAttachablesEndpoint:
					metadata, err := engine.ClientMetadataFromHTTPHeaders(r.Header)
					require.NoError(t, err)
					require.Equal(t, "interactive-cli", metadata.ClientID)
					require.Contains(t, r.Header.Values(engine.SessionMethodNameMetaKey), "/dagger.prompt.Prompt/PromptSelect")
					conn, _, err := w.(http.Hijacker).Hijack()
					require.NoError(t, err)
					_, err = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: h2c\r\n\r\n")
					require.NoError(t, err)
					_, err = io.ReadFull(conn, make([]byte, 1))
					require.NoError(t, err)
					var dialed bool
					cc, err := grpc.NewClient("passthrough:///nested-prompts",
						grpc.WithTransportCredentials(insecure.NewCredentials()),
						grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
							if dialed {
								return nil, errors.New("attachable channel closed")
							}
							dialed = true
							return conn, nil
						}))
					require.NoError(t, err)
					connections <- cc
				case engine.InitEndpoint:
					defer close(initDone)
					select {
					case promptConn = <-connections:
					case <-r.Context().Done():
						return
					}
					prompts := prompt.NewPromptClient(promptConn)
					answer, err := prompts.PromptBool(r.Context(), &prompt.BoolRequest{Prompt: "Include untracked files?", Default: true})
					require.NoError(t, err)
					require.False(t, answer.Response, "the CLI's No must override the default")
					_, err = prompts.PromptSelect(r.Context(), &prompt.SelectRequest{
						Prompt: "Include untracked files?", DefaultChoice: "skip",
						Choices: []*prompt.SelectChoice{{Id: "include", Label: "Include"}, {Id: "skip", Label: "Skip"}},
					})
					require.ErrorContains(t, err, "user canceled selection")
					if failInit {
						panic(http.ErrAbortHandler)
					}
				case engine.ShutdownEndpoint:
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			server.Config.Protocols = new(http.Protocols)
			server.Config.Protocols.SetHTTP1(true)
			server.Config.Protocols.SetUnencryptedHTTP2(true)
			server.Start()
			defer server.Close()
			t.Setenv("DAGGER_SESSION_PORT", strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port))
			t.Setenv("DAGGER_SESSION_TOKEN", "nested-token")
			t.Setenv(engine.NestedClientIDEnv, "bootstrap")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			c, err := Connect(ctx, Params{ID: "interactive-cli", PromptHandler: handler})
			if failInit {
				require.ErrorContains(t, err, "initialize nested client")
			} else {
				require.NoError(t, err)
				require.NoError(t, c.Close())
			}
			select {
			case <-initDone:
			case <-ctx.Done():
				t.Fatal("init did not reach the nested prompt channel")
			}
			require.NotNil(t, promptConn)
			defer promptConn.Close()
			require.Eventually(t, func() bool { return promptConn.GetState() != connectivity.Ready }, time.Second, time.Millisecond,
				"both close and failed Connect must stop the reverse attachable channel")
			require.Equal(t, 1, handler.boolPrompts)
			require.Equal(t, 1, handler.forms)
		})
	}
}

func TestNestedClientPreservesBootstrapWithoutLocalPrompt(t *testing.T) {
	for _, test := range []struct {
		name    string
		execID  string
		handler prompt.PromptHandler
	}{
		{name: "headless exec", execID: "bootstrap"},
		{name: "dagger run", handler: &nestedPromptHandler{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case engine.InitEndpoint, engine.ShutdownEndpoint:
				default:
					t.Errorf("must retain outer attachables, got %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			server.Config.Protocols = new(http.Protocols)
			server.Config.Protocols.SetHTTP1(true)
			server.Config.Protocols.SetUnencryptedHTTP2(true)
			server.Start()
			defer server.Close()
			t.Setenv("DAGGER_SESSION_PORT", strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port))
			t.Setenv(engine.NestedClientIDEnv, test.execID)
			c, err := Connect(t.Context(), Params{PromptHandler: test.handler})
			require.NoError(t, err)
			require.Nil(t, c.sessionSrv)
			require.NoError(t, c.Close())
		})
	}
}

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

func TestClientMetadataReportsCloudEngine(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		runnerHost string
		want       bool
	}{
		{name: "cloud", runnerHost: engine.DefaultCloudRunnerHost, want: true},
		{name: "local", runnerHost: "unix:///var/run/dagger.sock", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &Client{Params: Params{RunnerHost: test.runnerHost}}
			require.Equal(t, test.want, client.clientMetadata().CloudEngine)
		})
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
					if err := enginetel.WriteLiveHello(&stream, 0); err != nil {
						return nil, err
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
					// The hello echoes the resume cursor, which must not trip the
					// client's non-increasing cursor check.
					if err := enginetel.WriteLiveHello(&stream, 7); err != nil {
						return nil, err
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

// The engine announces a row it had to skip (one too large for any frame) as
// an empty data frame, so the client's cursor must advance on it and the
// terminal cursor must then match.
func TestOTLPConsumerAdvancesCursorOnEmptyFrame(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	require.NoError(t, enginetel.WriteLiveHello(&stream, 0))
	require.NoError(t, enginetel.WriteLiveFrame(&stream, 3, []byte("batch one")))
	require.NoError(t, enginetel.WriteLiveFrame(&stream, 4, nil))
	require.NoError(t, enginetel.WriteLiveTerminal(&stream, 4))

	var requests int
	httpClient := &httpClient{
		inner: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
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
		path:       "/v1/logs",
		eg:         telemetryGroup,
	}
	var batches [][]byte
	require.NoError(t, consumer.Consume(context.Background(), func(payload []byte, _ liveTelemetryEncoding) error {
		batches = append(batches, bytes.Clone(payload))
		return nil
	}))
	require.NoError(t, telemetryGroup.Wait())
	require.Equal(t, [][]byte{[]byte("batch one"), {}}, batches)
	require.Equal(t, 1, requests, "the terminal cursor matched, so no reconnect")
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
				if err := enginetel.WriteLiveHello(&stream, 0); err != nil {
					return nil, err
				}
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
