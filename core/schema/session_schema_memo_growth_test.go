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
//     every asModule that executes. The memo keys core by its base and view,
//     so a reload of a module whose dependencies did not change reuses the
//     server the session built.
//   - nested clients: every client has its own query root, and a server is
//     bound to its root, so the same module set resolved from N nested
//     clients builds N servers.
//   - control: the same builder identity resolves to one server.
//
// Past the memo's bound the session keeps only the servers it used most
// recently; TestSchemaBuilderMemoBounded covers that without building
// hundreds of servers.
//
// Each scenario resolves the dependency schema of a module that depends on
// the core API alone (the common case: moduleTypesDefinition and the SDK
// runtimes ask for it on every asModule) and describes it, as
// SchemaIntrospectionJSONFileForModule does. It reports how many servers were
// built and the heap the session retains after a GC.
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
		built    int
		retained int64
	}
	run := func(name string, loads int, deps func(i int) *core.SchemaBuilder) result {
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
		built := len(seen)
		clear(seen)
		retained := int64(liveHeap()) - int64(heapBefore)
		runtime.KeepAlive(session)
		srv.session = nil
		t.Logf("%-20s %3d loads -> %3d servers built, %7.1f MiB retained by the session (%6.1f KiB, %s per load)",
			name, loads, built, float64(retained)/(1<<20), float64(retained)/float64(loads)/(1<<10),
			(elapsed / time.Duration(loads)).Round(time.Microsecond))
		return result{built: built, retained: retained}
	}

	sameRoot := clientRoot
	control := run("control", loads, func(int) *core.SchemaBuilder {
		return core.NewSchemaBuilder(sameRoot, []core.Mod{sharedCore})
	})
	reload := run("reload", loads, func(int) *core.SchemaBuilder {
		// What defaultSchemaBuilderForEngineVersion hands each asModule.
		return core.NewSchemaBuilder(sameRoot, []core.Mod{sharedCore.WithView(view)})
	})
	nested := run("nested clients", loads, func(int) *core.SchemaBuilder {
		return core.NewSchemaBuilder(core.NewRoot(srv), []core.Mod{sharedCore})
	})

	require.Equal(t, 1, control.built, "one builder identity, one server")
	require.Equal(t, 1, reload.built,
		"a reload whose dependencies did not change reuses the session's server")
	require.Equal(t, loads, nested.built,
		"servers are bound to their root, so each nested client builds its own")
	// A forked core schema is well over 64 KiB.
	require.Greater(t, nested.retained, int64(loads-1)*(64<<10),
		"the session keeps the servers it built")
}

func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}
