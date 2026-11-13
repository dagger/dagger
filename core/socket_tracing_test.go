package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"path/filepath"
	"testing"
	"time"

	engineclient "github.com/dagger/dagger/engine/client"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"google.golang.org/grpc"
)

// Exercise the actual forwarding RPC without tracing interceptors, as used by
// the client's attachable server. Client connection spans must still join the
// engine's trace, including when identity lookup is canceled.
func TestSocketAgentTracing(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	for _, stage := range []string{"success", "attachable_lookup", "identity_reply"} {
		t.Run(stage, func(t *testing.T) {
			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			keyring := agent.NewKeyring()
			require.NoError(t, keyring.Add(agent.AddedKey{PrivateKey: priv}))
			publicKey, err := ssh.NewPublicKey(pub)
			require.NoError(t, err)

			release := make(chan struct{})
			defer close(release)
			identities := &tracedTestAgent{Agent: keyring, entered: make(chan struct{}, 1)}
			if stage == "identity_reply" {
				identities.release = release
			}
			path := filepath.Join(t.TempDir(), "agent.sock")
			listener, err := net.Listen("unix", path)
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = agent.ServeAgent(identities, conn)
			}()

			conn := newTestAttachableConn(t, func(server *grpc.Server) {
				engineclient.SocketProvider{EnableHostNetworkAccess: true}.Register(server)
			})
			ctx, _ := newSessionResourceFallbackTestContext(t, nil)
			server := &socketTraceTestServer{
				mockServer: mockServer{attachables: map[string]*grpc.ClientConn{"source-client": conn}},
				blocked:    stage == "attachable_lookup",
				entered:    make(chan struct{}, 1),
			}
			ctx = ContextWithQuery(ctx, &Query{Server: server})
			ctx, root := provider.Tracer("test").Start(ctx, "fingerprint probe")
			defer root.End()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			type result struct {
				fingerprints []string
				err          error
			}
			done := make(chan result, 1)
			go func() {
				fingerprints, err := (&Socket{
					Kind: SocketKindUnixOpaque, URLVal: "unix://" + path, SourceClientID: "source-client",
				}).AgentFingerprints(ctx)
				done <- result{fingerprints, err}
			}()
			if stage != "success" {
				entered := server.entered
				if stage == "identity_reply" {
					entered = identities.entered
				}
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("lookup did not reach the selected boundary")
				}
				cancel()
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("identity lookup did not finish")
			}
			if stage == "success" {
				require.NoError(t, got.err)
				require.Equal(t, []string{ssh.FingerprintSHA256(publicKey)}, got.fingerprints)
			} else {
				require.ErrorIs(t, got.err, context.Canceled)
				require.EqualError(t, got.err, "failed to list SSH agent identities: context canceled")
			}

			spans := map[string]sdktrace.ReadOnlySpan{}
			for _, span := range recorder.Ended() {
				if span.SpanContext().TraceID() == root.SpanContext().TraceID() {
					spans[span.Name()] = span
					require.Equal(t, root.SpanContext().SpanID(), span.Parent().SpanID(), span.Name())
					attrs := map[string]any{}
					for _, attr := range span.Attributes() {
						attrs[string(attr.Key)] = attr.Value.AsInterface()
					}
					require.Equal(t, true, attrs[telemetry.UIInternalAttr], span.Name())
					if span.Name() == "socket attachable lookup" {
						require.Equal(t, "source-client", attrs["dagger.io/socket.source_client_id"])
					}
				}
			}
			require.Contains(t, spans, "socket attachable lookup")
			require.Contains(t, spans, "SSH agent identity round trip")
			if stage == "attachable_lookup" {
				require.NotContains(t, spans, "connect client socket")
				require.Equal(t, codes.Error, spans["socket attachable lookup"].Status().Code)
			} else {
				require.Contains(t, spans, "connect client socket")
				require.NotEqual(t, codes.Error, spans["socket attachable lookup"].Status().Code)
			}
			if stage != "success" {
				require.Equal(t, codes.Error, spans["SSH agent identity round trip"].Status().Code)
			}
		})
	}
}

type socketTraceTestServer struct {
	mockServer
	blocked bool
	entered chan struct{}
}

func (s *socketTraceTestServer) SpecificClientAttachableConn(ctx context.Context, clientID string, opts SpecificClientAttachableConnOpts) (*grpc.ClientConn, bool, error) {
	s.entered <- struct{}{}
	if s.blocked {
		<-ctx.Done()
		return nil, false, ctx.Err()
	}
	return s.mockServer.SpecificClientAttachableConn(ctx, clientID, opts)
}

type tracedTestAgent struct {
	agent.Agent
	entered chan struct{}
	release <-chan struct{}
}

func (a *tracedTestAgent) List() ([]*agent.Key, error) {
	a.entered <- struct{}{}
	if a.release != nil {
		<-a.release
	}
	return a.Agent.List()
}
