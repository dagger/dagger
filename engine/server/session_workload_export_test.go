package server

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// Session shutdown must not close the engine-owned workload export while another
// session still uses it. Exercise session initialization and teardown with a
// real receiver, rather than inspecting the shared processor wrappers.
func TestSessionShutdownKeepsWorkloadExportOpen(t *testing.T) {
	receiver := newCloudReceiver(t, false)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	for signal, path := range map[string]string{"TRACES": "/v1/traces", "METRICS": "/v1/metrics"} {
		t.Setenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT", receiver.URL+path)
		t.Setenv("OTEL_EXPORTER_OTLP_"+signal+"_PROTOCOL", "http/protobuf")
	}
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "1000")
	workloadExport, err := enginetel.NewWorkloadExport(t.Context(), "test-engine")
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, workloadExport.Shutdown(ctx))
	})

	srv := &Server{
		clientDBs:       clientdb.NewDBs(t.TempDir()),
		wcprofSpanCount: newWcprofSpanCounter(),
		workloadExport:  workloadExport,
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
		ctx, span := sess.tracerProvider.Tracer("dagger.io/core").Start(ctx, name,
			trace.WithAttributes(attribute.String(telemetry.DagDigestAttr, "recipe-"+name)))
		var rec log.Record
		rec.SetTimestamp(time.Now())
		rec.SetBody(log.StringValue(name))
		sess.loggerProvider.Logger("test").Emit(ctx, rec)
		span.End()
	}
	waitFor := func(name string) {
		t.Helper()
		require.Eventually(t, func() bool {
			_, spans, _, _ := receiver.snapshot()
			return slices.Contains(spans, name)
		}, 5*time.Second, 10*time.Millisecond, "session trace must reach the receiver: %s", name)
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
	require.NoError(t, second.shutdownTelemetry(ctx))
	require.NoError(t, workloadExport.Shutdown(ctx))
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	require.Empty(t, receiver.logBodies, "application logs must never reach the workload export")
}
