package schema

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// memoGrowthServer serves one session memo to every client, as a dagger
// session does (Server.SessionSchemaBuilderMemo).
type memoGrowthServer struct {
	*currentTypeDefsTestServer
	session *core.SchemaBuilderMemo
}

func (s *memoGrowthServer) SessionSchemaBuilderMemo(context.Context) (*core.SchemaBuilderMemo, error) {
	return s.session, nil
}

// TestSessionSchemaMemoGrowth measures what a session's schema memo keeps when
// the same module set is resolved again, the way a long-lived session does:
//
//   - reload: a module's Deps comes from defaultSchemaBuilderForEngineVersion,
//     which wraps the core module in a fresh *CoreMod (CoreMod.WithView) on
//     every asModule that executes. The memo keys core modules by pointer, so
//     each reload of a module whose dependencies did not change builds and
//     keeps another server for an identical schema.
//   - nested clients: every client has its own query root, and the memo keys
//     the root by pointer, so the same module set resolved from N nested
//     clients builds and keeps N servers.
//   - control: the same builder identity resolves to one server.
//
// Each scenario resolves the dependency schema of a module that depends on
// the core API alone (the common case: moduleTypesDefinition and the SDK
// runtimes ask for it on every asModule) and describes it, as
// SchemaIntrospectionJSONFileForModule does. It reports how many servers the
// session holds and the heap they retain after a GC.
func TestSessionSchemaMemoGrowth(t *testing.T) {
	const loads = 40
	ctx := context.Background()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close(context.Background()) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "memo-growth-client",
		SessionID: "memo-growth-session",
	})

	srv := &memoGrowthServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{}}
	clientRoot := core.NewRoot(srv)
	ctx = core.ContextWithQuery(ctx, clientRoot)
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	view := call.View(engine.Version)
	sharedCore := base.CoreMod(view)

	type result struct {
		servers  int
		retained int64
	}
	run := func(name string, deps func(i int) *core.SchemaBuilder) result {
		t.Helper()
		session := core.NewSchemaBuilderMemo()
		srv.session = session
		// Warm the core view and the shared description cache outside the
		// measurement, so it counts only what each resolution adds.
		warm, err := core.NewSchemaBuilder(clientRoot, []core.Mod{sharedCore}).Clone().Schema(ctx)
		require.NoError(t, err)
		warm.Schema()

		heapBefore := liveHeap()
		seen := map[*dagql.Server]struct{}{}
		start := time.Now()
		for i := range loads {
			built, err := deps(i).Schema(ctx)
			require.NoError(t, err, name)
			built.Schema() // describe it, as introspection does
			seen[built] = struct{}{}
		}
		elapsed := time.Since(start)
		servers := len(seen)
		clear(seen)
		retained := int64(liveHeap()) - int64(heapBefore)
		runtime.KeepAlive(session)
		srv.session = nil
		t.Logf("%-16s %3d loads -> %3d servers kept by the session memo, %7.1f MiB retained (%6.1f KiB, %s per load)",
			name, loads, servers, float64(retained)/(1<<20), float64(retained)/float64(loads)/(1<<10),
			(elapsed / loads).Round(time.Microsecond))
		return result{servers: servers, retained: retained}
	}

	sameRoot := clientRoot
	control := run("control", func(int) *core.SchemaBuilder {
		return core.NewSchemaBuilder(sameRoot, []core.Mod{sharedCore})
	})
	reload := run("reload", func(int) *core.SchemaBuilder {
		// What defaultSchemaBuilderForEngineVersion hands each asModule.
		return core.NewSchemaBuilder(sameRoot, []core.Mod{sharedCore.WithView(view)})
	})
	nested := run("nested clients", func(int) *core.SchemaBuilder {
		return core.NewSchemaBuilder(core.NewRoot(srv), []core.Mod{sharedCore})
	})

	require.Equal(t, 1, control.servers, "one builder identity, one server")
	require.Equal(t, loads, reload.servers,
		"each reload builds and keeps another server for the same schema (core module keyed by pointer)")
	require.Equal(t, loads, nested.servers,
		"each nested client builds and keeps another server for the same schema (root keyed by pointer)")
	// A forked core schema is well over 64 KiB; the control keeps one.
	perLoad := int64(loads-1) * (64 << 10)
	require.Greater(t, reload.retained-control.retained, perLoad,
		"the session retains a forked core schema per reload")
	require.Greater(t, nested.retained-control.retained, perLoad,
		"the session retains a forked core schema per nested client")
}

func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}
