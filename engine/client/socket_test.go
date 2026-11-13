package client

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/metadata"
)

func TestSocketProxyConnectTracing(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	ctx, parent := provider.Tracer("test").Start(t.Context(), "engine lookup")
	defer parent.End()
	md := metadata.MD{}
	telemetry.Propagator.Inject(ctx, metadataCarrier(md))
	stream := &socketTraceTestStream{ctx: metadata.NewIncomingContext(t.Context(), md)}

	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	dialErr := errors.New("test dial failure")
	proxy := &socketProxy{
		network: "unix",
		addr:    "/private/agent.sock",
		dialer: func(_, _ string) (net.Conn, error) {
			close(entered)
			<-release
			return nil, dialErr
		},
	}
	done := make(chan error, 1)
	go func() { done <- proxy.ForwardAgent(stream) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("socket dial did not start")
	}
	started := recorder.Started()
	require.Len(t, started, 2)
	connecting := started[1]
	require.Equal(t, "connect client socket", connecting.Name())
	require.Equal(t, parent.SpanContext().TraceID(), connecting.SpanContext().TraceID())
	require.Equal(t, parent.SpanContext().SpanID(), connecting.Parent().SpanID())
	require.Empty(t, recorder.Ended(), "connection span must remain open while dialing")
	release <- struct{}{}
	select {
	case err := <-done:
		require.ErrorIs(t, err, dialErr)
		require.EqualError(t, err, "dialer failed: test dial failure")
	case <-time.After(5 * time.Second):
		t.Fatal("socket dial did not return")
	}
	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, codes.Error, ended[0].Status().Code)
	require.Equal(t, dialErr.Error(), ended[0].Status().Description)
	attrs := map[string]any{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	require.Equal(t, true, attrs[telemetry.UIInternalAttr])
	require.Equal(t, "unix", attrs["network.transport"])
}

type socketTraceTestStream struct {
	sshforward.SSH_ForwardAgentServer
	ctx context.Context
}

func (s *socketTraceTestStream) Context() context.Context { return s.ctx }
