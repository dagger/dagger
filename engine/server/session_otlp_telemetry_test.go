package server

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
)

// Session shutdown must not close the engine-owned destination while another
// session still uses it. Exercise session initialization and teardown with a
// real receiver, rather than inspecting the shared processor wrappers.
func TestSessionShutdownKeepsOTLPDestinationOpen(t *testing.T) {
	t.Parallel()
	receiver := newCloudReceiver(t, false)
	destination, err := enginetel.NewOTLPDestination(t.Context(), enginetel.OTLPOptions{
		Endpoint: receiver.URL,
		Signals:  []string{"traces", "logs"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, destination.Shutdown(ctx))
	})

	srv := &Server{
		clientDBs:       clientdb.NewDBs(t.TempDir()),
		wcprofSpanCount: newWcprofSpanCounter(),
		otlpDestination: destination,
	}
	srv.telemetryPubSub = NewPubSub(srv)
	t.Cleanup(func() { require.NoError(t, srv.clientDBs.Close()) })

	newSession := func(id string) (*daggerSession, context.Context) {
		md := &engine.ClientMetadata{SessionID: id, ClientID: id + "-client"}
		client := &clientRuntime{clientRecord: &clientRecord{clientID: md.ClientID, clientMetadata: md}}
		sess := &daggerSession{
			sessionID:          id,
			mainClientCallerID: client.clientID,
			clientRuntimes:     map[string]*clientRuntime{client.clientID: client},
			telemetryPubSub:    srv.telemetryPubSub,
		}
		installTestClientRecords(sess)
		srv.initializeSessionTelemetry(sess, md)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, sess.shutdownTelemetry(ctx))
		})
		return sess, engine.ContextWithClientMetadata(t.Context(), md)
	}
	first, firstCtx := newSession("first")
	second, secondCtx := newSession("second")

	emit := func(sess *daggerSession, ctx context.Context, name string) {
		ctx, span := sess.tracerProvider.Tracer("test").Start(ctx, name)
		var rec log.Record
		rec.SetTimestamp(time.Now())
		rec.SetBody(log.StringValue(name))
		sess.loggerProvider.Logger("test").Emit(ctx, rec)
		span.End()
	}
	waitFor := func(name string) {
		t.Helper()
		require.Eventually(t, func() bool {
			_, spans, logs, _ := receiver.snapshot()
			return slices.Contains(spans, name) && slices.Contains(logs, name)
		}, 5*time.Second, 10*time.Millisecond, "session trace and log must reach the receiver: %s", name)
	}

	emit(first, firstCtx, "first-before-shutdown")
	emit(second, secondCtx, "second-before-shutdown")
	waitFor("first-before-shutdown")
	waitFor("second-before-shutdown")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, first.shutdownTelemetry(ctx))

	emit(second, secondCtx, "second-after-first-shutdown")
	waitFor("second-after-first-shutdown")
}
