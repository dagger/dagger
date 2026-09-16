package schema

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func TestSchemaFileLazy(t *testing.T) {
	store := testutil.NewStore(t)
	observed := &scratchObservedManager{SnapshotManager: store.Manager}
	store.Manager = observed
	path := filepath.Join(t.TempDir(), "schema.db")
	open := func() (context.Context, *dagql.Cache, *dagql.Server) {
		ctx, cache, srv := scratchTestCache(t, store, path, "schema")
		srv.InstallObject(dagql.NewClass[*core.File](srv))
		dagql.Fields[*core.Query]{dagql.NodeFunc("__schemaJSONFile", (&querySchema{}).schemaJSONFile).View(AllVersion).IsPersistable().WithInput(dagql.CurrentSchemaInput).WithInput(engineDefaultPlatformInput)}.Install(srv)
		return ctx, cache, srv
	}
	ctx, cache, srv := open()
	var result dagql.ObjectResult[*core.File]
	require.NoError(t, srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "__schemaJSONFile"}))
	lazy := result.Self().Lazy.(*core.FileBlobLazy)
	require.False(t, lazy.IsEvaluated())
	require.Zero(t, observed.calls.Load(), "constructing a schema File must not open scratch")
	expected, err := getSchemaJSON(nil, nil, srv.View, srv)
	require.NoError(t, err)
	require.Equal(t, expected, lazy.Contents)
	require.Equal(t, "schema.json", lazy.Filename)
	record, err := cache.CapturePersistedRecord(ctx, result)
	require.NoError(t, err)
	inputs := []string{}
	for _, input := range record.Call.ImplicitInputs {
		inputs = append(inputs, input.Name)
	}
	require.ElementsMatch(t, []string{"cachePerSchema", "engineDefaultPlatform"}, inputs)
	var payload struct{ Form, LazyKind string }
	require.NoError(t, json.Unmarshal(record.Envelope.ObjectJSON, &payload))
	require.Equal(t, "lazy", payload.Form)
	require.Equal(t, "file.blob", payload.LazyKind)
	require.NoError(t, cache.ReleaseSession(ctx, "schema"))
	require.NoError(t, cache.Close(ctx))
	store.Reload(t)
	observed = &scratchObservedManager{SnapshotManager: store.Manager}
	store.Manager = observed
	ctx, cache, srv = open()
	restored, err := cache.LoadResultByResultID(ctx, "schema", srv, record.ResultID)
	require.NoError(t, err)
	result = restored.(dagql.ObjectResult[*core.File])
	lazy = result.Self().Lazy.(*core.FileBlobLazy)
	require.False(t, lazy.IsEvaluated())
	require.Zero(t, observed.calls.Load())
	require.Equal(t, expected, lazy.Contents)
	before, err := cache.CapturePersistedRecord(ctx, result)
	require.NoError(t, err)
	require.JSONEq(t, string(record.Envelope.ObjectJSON), string(before.Envelope.ObjectJSON))
	got, err := result.Self().Contents(ctx, result, nil, nil)
	require.NoError(t, err)
	require.Equal(t, expected, got)
	name, set := result.Self().File.Peek()
	require.True(t, set)
	require.Equal(t, "/schema.json", name)
	require.Equal(t, "linux/arm64", result.Self().Platform.Format())
	require.Same(t, lazy, result.Self().Lazy)
	require.True(t, lazy.IsEvaluated())
	require.EqualValues(t, 1, observed.calls.Load())
	require.NoError(t, cache.Evaluate(ctx, result))
	require.EqualValues(t, 1, observed.calls.Load())
	t.Logf("schema bytes=%d pending payload bytes=%d scratch calls before first use=0 after first use=1", len(expected), len(record.Envelope.ObjectJSON))
}
