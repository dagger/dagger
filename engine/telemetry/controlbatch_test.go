package telemetry

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/stretchr/testify/require"
	logapi "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func TestControlProtectedFromOrdinaryOverflow(t *testing.T) {
	ordinary := &blockingLogExporter{started: make(chan struct{}), release: make(chan struct{})}
	protected := &captureLogExporter{}
	control := NewControlBatchProcessor(protected)
	batch := NewLogBatchProcessor(ordinary)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(control), sdklog.WithProcessor(WithoutCallPayloads(batch)))
	logger := provider.Logger("test")
	for range LogQueueSize * 2 {
		var rec logapi.Record
		rec.SetBody(logapi.StringValue("noise"))
		logger.Emit(t.Context(), rec)
	}
	a := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "session", Trace: "trace", Incarnation: "incarnation"}, Handle: "worker"}, Revision: 9, State: "IDLE", Digest: "snapshot"}
	logger.Emit(t.Context(), a.Record())
	require.NoError(t, control.ForceFlush(t.Context()))
	protected.mu.Lock()
	require.Len(t, protected.records, 1)
	got, _, err := agentcontrol.Decode(protected.records[0])
	require.NoError(t, err)
	require.Equal(t, a.Revision, got.Revision)
	protected.mu.Unlock()
	close(ordinary.release)
	require.NoError(t, provider.Shutdown(t.Context()))
	ordinary.mu.Lock()
	defer ordinary.mu.Unlock()
	// Ordinary output cannot contain a second control copy (and its queue may drop noise).
	require.LessOrEqual(t, ordinary.exported, LogQueueSize*2)
}

func TestControlCanceledDrainRetryLifecycle(t *testing.T) {
	t.Parallel()

	for _, shutdown := range []bool{false, true} {
		name := "flush"
		if shutdown {
			name = "shutdown"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				exporter := &flakyLogExporter{failures: CallPayloadMaxExportAttempts}
				proc := NewControlBatchProcessor(exporter)
				provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
				rec := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "session", Trace: "trace", Incarnation: "incarnation"}, Handle: "worker"}, Revision: 1, State: "IDLE", Digest: "snapshot"}.Record()
				provider.Logger("test").Emit(t.Context(), rec)
				time.Sleep(CallPayloadExportDelay)
				synctest.Wait()
				attempts, _, _ := exporter.stats()
				require.Equal(t, 1, attempts)

				ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
				defer cancel()
				drain := proc.ForceFlush
				if shutdown {
					drain = proc.Shutdown
				}
				require.ErrorIs(t, drain(ctx), context.DeadlineExceeded)
				synctest.Wait()
				time.Sleep(CallPayloadMaxExportAttempts * CallPayloadRetryMaxDelay)
				synctest.Wait()
				attempts, bodies, _ := exporter.stats()
				require.Empty(t, bodies)
				if shutdown {
					require.Equal(t, 2, attempts, "shutdown must stop background retries")
					require.ErrorIs(t, proc.ForceFlush(t.Context()), context.DeadlineExceeded)
					require.ErrorIs(t, proc.Shutdown(t.Context()), context.DeadlineExceeded)
				} else {
					require.Equal(t, CallPayloadMaxExportAttempts, attempts, "flush cancellation must preserve the retry budget")
					require.ErrorContains(t, proc.ForceFlush(t.Context()), "dropping 1 protected records")
					require.ErrorContains(t, proc.Shutdown(t.Context()), "dropping 1 protected records")
				}
			})
		})
	}
}

func TestControlDrainRetriesPersistence(t *testing.T) {
	exporter := &flakyLogExporter{failures: 2}
	proc := NewControlBatchProcessor(exporter)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
	rec := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "session", Trace: "trace", Incarnation: "incarnation"}, Handle: "worker"}, Revision: 1, State: "IDLE", Digest: "snapshot"}.Record()
	rec.SetBody(logapi.StringValue("control"))
	provider.Logger("test").Emit(t.Context(), rec)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, proc.Shutdown(ctx))
	attempts, _, _ := exporter.stats()
	require.Equal(t, 3, attempts)
	require.NoError(t, proc.ForceFlush(ctx))
}
