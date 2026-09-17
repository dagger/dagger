package core

import (
	"encoding/json"
	"os"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/archive"
	engineclient "github.com/dagger/dagger/engine/client"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Exercise session teardown, on-disk archival, HTTP bootstrap, portable recipe
// reconstruction, and restoration into a new session without a provider call.
func TestEngineArchiveRoundTrip(t *testing.T) {
	// Main clients own archives. Connect directly to the test engine, not through
	// the test runner's nested session. This test intentionally is not parallel.
	if value, ok := os.LookupEnv("DAGGER_SESSION_PORT"); ok {
		t.Setenv("DAGGER_SESSION_PORT", value)
		require.NoError(t, os.Unsetenv("DAGGER_SESSION_PORT"))
	}
	provider := sdktrace.NewTracerProvider()
	defer provider.Shutdown(t.Context())
	ctx, span := provider.Tracer("archive-integration").Start(t.Context(), "source session", trace.WithNewRoot())
	defer span.End()
	traceID := span.SpanContext().TraceID().String()
	params := engineclient.Params{
		ID: identity.NewID(), RunnerHost: "unix:///run/dagger-engine.sock",
		ArchiveTelemetry: true, ArchiveTraceID: traceID,
	}
	source, err := engineclient.Connect(ctx, params)
	require.NoError(t, err)
	sourceClosed := false
	t.Cleanup(func() {
		if !sourceClosed {
			source.Close()
		}
	})
	var spawned struct {
		LLM struct{ WithPrompt struct{ Spawn string } }
	}
	require.NoError(t, source.Dagger().Do(ctx, &dagger.Request{
		Query:     `query($model: String!) { llm(model: $model) { withPrompt(prompt: "remember this pending prompt") { spawn(name: "archive-probe", state: PAUSED) } } }`,
		Variables: map[string]any{"model": emptyReplayModel},
	}, &dagger.Response{Data: &spawned}))
	require.NotEmpty(t, spawned.LLM.WithPrompt.Spawn)
	require.NoError(t, source.ArchiveClient().UpdateMetadata(ctx, traceID, archive.MetadataUpdate{Title: "offline archive test"}))
	require.NoError(t, source.Close())
	sourceClosed = true

	resumeCtx, resumeSpan := provider.Tracer("archive-integration").Start(t.Context(), "new session", trace.WithNewRoot())
	defer resumeSpan.End()
	resumed, err := engineclient.Connect(resumeCtx, engineclient.Params{ID: identity.NewID(), RunnerHost: params.RunnerHost})
	require.NoError(t, err)
	defer resumed.Close()
	manifests, err := resumed.ArchiveClient().ListAll(resumeCtx, archive.ListOptions{})
	require.NoError(t, err)
	var manifest archive.Manifest
	for _, candidate := range manifests {
		if candidate.TraceID == traceID {
			manifest = candidate
		}
	}
	require.Equal(t, archive.StateClosed, manifest.State, "archive failure: %s", manifest.Failure)
	require.Equal(t, "offline archive test", manifest.Title)

	db := dagui.NewDB()
	importer := enginetel.NewTraceImporter(enginetel.TraceImportSinks{Spans: db, Logs: db.LogExporter(), Metrics: db.MetricExporter()})
	var checkpoint core.AgentCheckpoint
	_, err = resumed.ArchiveClient().Bootstrap(resumeCtx, traceID, manifest.Generation, func(_ archive.BootstrapHeader, batch archive.BootstrapBatch) error {
		if batch.Traces != nil {
			return importer.ImportSpans(resumeCtx, batch.Traces)
		}
		for _, resource := range batch.Logs.GetResourceLogs() {
			for _, scope := range resource.GetScopeLogs() {
				if scope.GetScope().GetName() != telemetryattrs.AgentCheckpointInstrumentationScope {
					continue
				}
				for _, record := range scope.GetLogRecords() {
					var candidate core.AgentCheckpoint
					if err := json.Unmarshal(record.GetBody().GetBytesValue(), &candidate); err != nil {
						return err
					}
					if candidate.Final {
						checkpoint = candidate
					}
				}
			}
		}
		return importer.ImportLogs(resumeCtx, batch.Logs)
	})
	require.NoError(t, err)
	require.Equal(t, "archive-probe", checkpoint.Name)
	require.Equal(t, core.AgentStatePaused, checkpoint.PreTeardownState)
	snapshot, err := db.CallIDForDigest(checkpoint.SnapshotDigest)
	require.NoError(t, err)
	snapshotID, err := snapshot.Encode()
	require.NoError(t, err)
	var restored struct{ Node struct{ Spawn string } }
	require.NoError(t, resumed.Dagger().Do(resumeCtx, &dagger.Request{
		Query:     `query($id: ID!, $handle: String!) { node(id: $id) { ... on LLM { spawn(handle: $handle, name: "archive-probe", state: PAUSED) } } }`,
		Variables: map[string]any{"id": snapshotID, "handle": checkpoint.AgentID},
	}, &dagger.Response{Data: &restored}))
	require.NotEmpty(t, restored.Node.Spawn)
	var state struct{ Node struct{ State string } }
	require.NoError(t, resumed.Dagger().Do(resumeCtx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on Agent { state } } }`,
		Variables: map[string]any{"id": restored.Node.Spawn},
	}, &dagger.Response{Data: &state}))
	require.Equal(t, "PAUSED", state.Node.State)
}
