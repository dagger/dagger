package schema

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type fixtureTestServer struct {
	*currentTypeDefsTestServer
	fn *core.FunctionCall
}

func (s *fixtureTestServer) CurrentFunctionCall(context.Context) (*core.FunctionCall, error) {
	return s.fn, nil
}

func TestRemoteCacheFixture(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{SessionID: "fixture", ClientID: "client"})
	cache, err := dagql.NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	ctx = dagql.ContextWithCache(ctx, cache)
	facade := &fixtureTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{}, fn: &core.FunctionCall{Name: "report", ParentName: "Probe"}}
	q := core.NewRoot(facade)
	ctx = core.ContextWithQuery(ctx, q)
	srv, err := dagql.NewServer(ctx, q)
	require.NoError(t, err)
	facade.dag = srv
	t.Setenv(remoteCacheFixtureGate, "")
	require.NoError(t, installRemoteCacheFixture(srv))
	_, err = srv.Query(ctx, `{ _remoteCacheFixture(operation:"report") }`, nil)
	require.Error(t, err)
	for _, path := range []string{"relative", filepath.Join(t.TempDir(), "missing")} {
		t.Setenv(remoteCacheFixtureGate, path)
		require.Error(t, installRemoteCacheFixture(srv))
	}
	root := t.TempDir()
	t.Setenv(remoteCacheFixtureGate, root)
	require.NoError(t, installRemoteCacheFixture(srv))
	callOperation := func(server *dagql.Server, operation string) string {
		t.Helper()
		result, err := server.Query(ctx, `query($op:String!){_remoteCacheFixture(operation:$op)}`, map[string]any{"op": operation})
		require.NoError(t, err)
		raw, err := json.Marshal(result["_remoteCacheFixture"])
		require.NoError(t, err)
		var value string
		require.NoError(t, json.Unmarshal(raw, &value))
		return value
	}
	callOperation(srv, "recordBody")
	callOperation(srv, "recordBody")
	fork, err := srv.Fork(ctx, q)
	require.NoError(t, err)
	callOperation(fork, "recordBody")
	var report remoteCacheFixtureReport
	require.NoError(t, json.Unmarshal([]byte(callOperation(srv, "report")), &report))
	require.Len(t, report.Bodies, 1)
	require.Equal(t, uint64(3), report.Bodies[0].Count)
	// Restart diagnostics remain available without resolving a saved handle.
	require.NoError(t, os.WriteFile(filepath.Join(root, "persistence.json"), []byte(`{"persistenceResetReason":"unclean_shutdown","localCacheResetReason":"dagql_unclean_shutdown","removedPersistedRootCount":3}`), 0600))
	require.NoError(t, json.Unmarshal([]byte(callOperation(srv, "report")), &report))
	require.Equal(t, dagql.CachePersistenceResetUncleanShutdown, report.Persistence.PersistenceResetReason)
	require.Equal(t, "dagql_unclean_shutdown", report.Persistence.LocalCacheResetReason)
	require.Equal(t, 3, report.Persistence.RemovedPersistedRootCount)
	// Repeating the same GraphQL import must allocate another fresh root.
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*core.Address]{}))
	address := &core.Address{Value: "recorded"}
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "fixtureAddress", Type: dagql.NewResultCallType(address.Type())}
	attached, err := cache.GetOrInitCall(ctx, "fixture", srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(address, srv, frame)
	})
	require.NoError(t, err)
	id, err := attached.ID()
	require.NoError(t, err)
	handle, err := id.Encode()
	require.NoError(t, err)
	execute := func(op string, ids []string) []remoteCacheFixtureMapping {
		t.Helper()
		variablesIDs := make([]any, len(ids))
		for i, id := range ids {
			variablesIDs[i] = id
		}
		values, err := srv.Query(ctx, `query($op:String!,$ids:[ID!]!){_remoteCacheFixture(operation:$op,path:"value.json",ids:$ids)}`, map[string]any{"op": op, "ids": variablesIDs})
		require.NoError(t, err)
		raw, err := json.Marshal(values["_remoteCacheFixture"])
		require.NoError(t, err)
		var text string
		require.NoError(t, json.Unmarshal(raw, &text))
		var mapping []remoteCacheFixtureMapping
		require.NoError(t, json.Unmarshal([]byte(text), &mapping))
		return mapping
	}
	exported := execute("export", []string{handle})
	require.Len(t, exported, 1)
	require.Equal(t, id.EngineResultID(), exported[0].ResultID)
	first, second := execute("import", []string{}), execute("import", []string{})
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.NotEqual(t, first[0].ResultID, second[0].ResultID)
	var bundle dagql.ValueBundle
	bundleRaw, err := os.ReadFile(filepath.Join(root, "bundles", "value.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(bundleRaw, &bundle))
	precise, err := fixtureMappings(bundle, []dagql.ImportedValue{{Ordinal: 1, ResultID: 9007199254740993}})
	require.NoError(t, err)
	var preciseID call.ID
	require.NoError(t, preciseID.Decode(precise[0].Handle))
	require.Equal(t, uint64(9007199254740993), preciseID.EngineResultID())
	childFrame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "fixtureChild", Type: frame.Type, Receiver: &dagql.ResultCallRef{ResultID: id.EngineResultID()}}
	child, err := cache.GetOrInitCall(ctx, "fixture", srv, &dagql.CallRequest{ResultCall: childFrame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(&core.Address{Value: "child"}, srv, childFrame)
	})
	require.NoError(t, err)
	childID, err := child.ID()
	require.NoError(t, err)
	childHandle, err := childID.Encode()
	require.NoError(t, err)
	execute("export", []string{childHandle})
	closure := execute("import", []string{})
	require.Len(t, closure, 2)
	rows, err := cache.TransferFixtureSnapshot(ctx, "fixture", nil)
	require.NoError(t, err)
	byID := map[uint64]dagql.TransferFixtureRow{}
	for _, row := range rows.Rows {
		byID[row.ResultID] = row
	}
	require.Equal(t, "fixtureChild", byID[closure[0].ResultID].Call.Field)
	require.Equal(t, closure[1].ResultID, byID[closure[0].ResultID].Call.Receiver.ResultID)
	require.Equal(t, "fixtureAddress", byID[closure[1].ResultID].Call.Field)
	// Root IDs alone cannot establish the dependency mapping. Validate the
	// base against an independently reported non-root row, then corrupt it.
	bundleRaw, err = os.ReadFile(filepath.Join(root, "bundles", "value.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(bundleRaw, &bundle))
	importedRoots := []dagql.ImportedValue{{Ordinal: closure[0].Ordinal, ResultID: closure[0].ResultID}}
	reported := []dagql.TransferFixtureRow{byID[closure[0].ResultID], byID[closure[1].ResultID]}
	_, err = fixtureImportedMappings(bundle, importedRoots, reported)
	require.NoError(t, err)
	reported[1].ResultID += 1000
	_, err = fixtureImportedMappings(bundle, importedRoots, reported)
	require.ErrorContains(t, err, "reported non-root")
	const count = 16
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for range count {
		wg.Go(func() {
			_, err := runRemoteCacheFixture(ctx, q, root, remoteCacheFixtureArgs{Operation: "recordBody"})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, json.Unmarshal([]byte(callOperation(srv, "report")), &report))
	require.Equal(t, uint64(count+3), report.Bodies[0].Count)
	for _, path := range []string{"", ".", "..", "../out", "/abs", "a/../out", "a/.", "a\x00b"} {
		_, err := runRemoteCacheFixture(ctx, q, root, remoteCacheFixtureArgs{Operation: "import", Path: path})
		require.Error(t, err)
	}
	for _, args := range []remoteCacheFixtureArgs{{Operation: "export", Path: "valid"}, {Operation: "report", Path: "valid"}, {Operation: "recordBody", Path: "valid"}, {Operation: "unknown"}} {
		_, err := runRemoteCacheFixture(ctx, q, root, args)
		require.Error(t, err)
	}
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bundles"), 0700))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "bundles", "escape")))
	br, err := fixtureRoot(root, "bundles")
	require.NoError(t, err)
	defer br.Close()
	require.Error(t, writeFixtureJSON(ctx, br, "escape/data.json", report))
	_, err = os.Stat(filepath.Join(outside, "data.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = runRemoteCacheFixture(canceled, q, root, remoteCacheFixtureArgs{Operation: "recordBody"})
	require.ErrorIs(t, err, context.Canceled)
	facade.fn = nil
	_, err = runRemoteCacheFixture(ctx, q, root, remoteCacheFixtureArgs{Operation: "recordBody"})
	require.ErrorContains(t, err, "current function")
	require.NoError(t, os.WriteFile(filepath.Join(root, "body-entries", ".tmp-interrupted"), []byte("bad"), 0600))
	callOperation(srv, "report")
	require.NoError(t, os.WriteFile(filepath.Join(root, "body-entries", "bad.json"), []byte(`{"count":999}`), 0600))
	_, err = runRemoteCacheFixture(ctx, q, root, remoteCacheFixtureArgs{Operation: "report"})
	require.Error(t, err)
}
