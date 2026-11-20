package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

// memoTestMod is a stand-in for a non-module Mod (like core), which the memo
// keys by instance.
type memoTestMod struct {
	Mod
	name string
	// same, if set, is another module this one is the same installed
	// module as.
	same Mod
}

func (m *memoTestMod) Name() string { return m.name }

func (m *memoTestMod) Same(other Mod) (bool, error) {
	o, _ := other.(*memoTestMod)
	return o == m || (o != nil && (m.same == Mod(o) || o.same == Mod(m))), nil
}

func TestSchemaBuilderMemo(t *testing.T) {
	a, b := &memoTestMod{name: "a"}, &memoTestMod{name: "b"}
	root := &Query{}
	base := NewSchemaBuilder(root, []Mod{a})

	t.Run("same entries share a builder", func(t *testing.T) {
		memo := NewSchemaBuilderMemo()
		first := memo.Get(base.Clone().Append(b))
		require.Same(t, first, memo.Get(base.Clone().Append(b)))
		require.Same(t, first, memo.Get(base.Append(b)))
	})

	t.Run("different entries do not", func(t *testing.T) {
		memo := NewSchemaBuilderMemo()
		withB := memo.Get(base.Append(b))
		require.NotSame(t, withB, memo.Get(base.Clone()))
		require.NotSame(t, withB, memo.Get(base.Prepend(b)), "entry order is part of the key")
		require.NotSame(t, withB, memo.Get(base.Append(&memoTestMod{name: "b"})),
			"a different instance with the same name is a different module")
		require.NotSame(t, withB, memo.Get(base.With(b, InstallOpts{Main: true})),
			"install options are part of the key")
		require.NotSame(t, withB, memo.Get(base.WithRoot(&Query{}).Append(b)),
			"servers are built on the root, so it is part of the key")
	})

	t.Run("equivalent core modules share a builder", func(t *testing.T) {
		// Core is keyed by its content (SchemaASTCacheKey), not its instance:
		// every module load wraps it anew.
		memo := NewSchemaBuilderMemo()
		first := memo.Get(NewSchemaBuilder(root, []Mod{&keyedInstallMod{key: "core@v1"}}).Append(b))
		require.Same(t, first, memo.Get(NewSchemaBuilder(root, []Mod{&keyedInstallMod{key: "core@v1"}}).Append(b)))
		require.NotSame(t, first, memo.Get(NewSchemaBuilder(root, []Mod{&keyedInstallMod{key: "core@v2"}}).Append(b)),
			"another view is another schema")
		require.NotSame(t, first, memo.Get(NewSchemaBuilder(&Query{}, []Mod{&keyedInstallMod{key: "core@v1"}}).Append(b)),
			"the root is still part of the key")
	})

	t.Run("a failed build is not reused", func(t *testing.T) {
		memo := NewSchemaBuilderMemo()
		failed := memo.Get(base.Append(b))
		failed.loadSchemaFailed.Store(true)
		replaced := memo.Get(base.Append(b))
		require.NotSame(t, failed, replaced)
		require.Same(t, replaced, memo.Get(base.Append(b)))
	})

	t.Run("memos are independent", func(t *testing.T) {
		require.NotSame(t,
			NewSchemaBuilderMemo().Get(base.Append(b)),
			NewSchemaBuilderMemo().Get(base.Append(b)))
	})

	t.Run("a nil memo does not memoize", func(t *testing.T) {
		var memo *SchemaBuilderMemo
		deps := base.Append(b)
		require.Same(t, deps, memo.Get(deps))
	})
}

// ctxInstallMod installs nothing, failing only when the installing context is
// done.
type ctxInstallMod struct {
	Mod
}

func (*ctxInstallMod) Name() string            { return "ctx" }
func (*ctxInstallMod) View() (call.View, bool) { return "", false }
func (*ctxInstallMod) Install(ctx context.Context, _ *dagql.Server, _ ...InstallOpts) error {
	return ctx.Err()
}

// A shared builder must not hand one caller's cancellation to the others: the
// schema is fine, only that caller gave up.
func TestSchemaBuilderCanceledLoadDoesNotStick(t *testing.T) {
	deps := NewSchemaBuilderMemo().Get(NewSchemaBuilder(&Query{}, []Mod{&ctxInstallMod{}}))

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := deps.Schema(canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, deps.loadSchemaFailed.Load(), "a canceled load is not a failed build")

	srv, err := deps.Schema(t.Context())
	require.NoError(t, err, "a later caller builds with its own context")
	again, err := deps.Schema(t.Context())
	require.NoError(t, err)
	require.Same(t, srv, again, "the built server is kept")
}

// memoTestServer hands out one session's schema builder memo, and a client
// memo of its own.
type memoTestServer struct {
	*mockServer
	client  *SchemaBuilderMemo
	session *SchemaBuilderMemo
}

func (s *memoTestServer) SchemaBuilderMemo(context.Context) (*SchemaBuilderMemo, error) {
	return s.client, nil
}

func (s *memoTestServer) SessionSchemaBuilderMemo(context.Context) (*SchemaBuilderMemo, error) {
	return s.session, nil
}

// A builder that is not client-owned, like a cached Module's Deps, never keeps
// the server it builds: the current session's memo does, so the server is
// built once per session, shared by its clients, and dies with it.
func TestSchemaBuilderServerLivesInSessionMemo(t *testing.T) {
	deps := NewSchemaBuilder(&Query{}, []Mod{&ctxInstallMod{}})
	session := NewSchemaBuilderMemo()
	client := func(session *SchemaBuilderMemo) context.Context {
		return ContextWithQuery(t.Context(), &Query{Server: &memoTestServer{mockServer: &mockServer{}, client: NewSchemaBuilderMemo(), session: session}})
	}

	srv, err := deps.Schema(client(session))
	require.NoError(t, err)
	again, err := deps.Schema(client(session))
	require.NoError(t, err)
	require.Same(t, srv, again, "the session's clients build the module set once")
	require.Nil(t, deps.lazilyLoadedServer, "the builder itself keeps no server")

	other, err := deps.Schema(client(NewSchemaBuilderMemo()))
	require.NoError(t, err)
	require.NotSame(t, srv, other, "another session builds its own")
}

// flakyCoreMod is a core module, identified by content, whose first fork
// fails once it is released.
type flakyCoreMod struct {
	Mod
	base    *dagql.Server
	forks   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (*flakyCoreMod) Name() string              { return ModuleName }
func (*flakyCoreMod) View() (call.View, bool)   { return "", false }
func (*flakyCoreMod) SchemaASTCacheKey() string { return "flaky-core" }
func (m *flakyCoreMod) ForkSchema(ctx context.Context, root *Query, _ call.View) (*dagql.Server, error) {
	if m.forks.Add(1) == 1 {
		close(m.entered)
		<-m.release
		return nil, errors.New("transient fork failure")
	}
	return m.base.Fork(ctx, root)
}

// Clients of a session share one server for a module set, whatever their
// roots. A client that waited on another client's build that failed keeps
// none of that failure: its next load builds the set again.
func TestSchemaBuilderSharedAcrossRoots(t *testing.T) {
	session := NewSchemaBuilderMemo()
	facade := &memoTestServer{mockServer: &mockServer{}, client: NewSchemaBuilderMemo(), session: session}
	base, err := dagql.NewServer(t.Context(), &Query{})
	require.NoError(t, err)
	coreMod := &flakyCoreMod{base: base, entered: make(chan struct{}), release: make(chan struct{})}
	client := func() (context.Context, *SchemaBuilder) {
		root := &Query{Server: facade}
		return ContextWithQuery(t.Context(), root), NewSchemaBuilder(root, []Mod{coreMod}).ClientOwned()
	}
	ctxA, servedA := client()
	ctxB, servedB := client()

	failedA := make(chan error, 1)
	go func() {
		_, err := servedA.Schema(ctxA)
		failedA <- err
	}()
	<-coreMod.entered
	waitedB := make(chan error, 1)
	go func() {
		_, err := servedB.Schema(ctxB)
		waitedB <- err
	}()
	time.Sleep(50 * time.Millisecond) // let b wait on a's build
	close(coreMod.release)
	require.ErrorContains(t, <-failedA, "transient fork failure")
	if err := <-waitedB; err != nil {
		require.ErrorContains(t, err, "transient fork failure", "b got a's failure")
	}

	srvB, err := servedB.Schema(ctxB)
	require.NoError(t, err, "a failure b waited on does not stick to b")
	srvA, err := servedA.Schema(ctxA)
	require.NoError(t, err, "nor to a")
	require.Same(t, srvA, srvB, "the clients share the server")
	require.NotSame(t, ctxA.Value(queryKey{}), srvA.Root().Unwrap(), "it is built on neither client's root")
	require.NotSame(t, ctxB.Value(queryKey{}), srvA.Root().Unwrap())

	deps, err := NewSchemaBuilder(&Query{Server: facade}, []Mod{coreMod}).Schema(ctxA)
	require.NoError(t, err)
	require.Same(t, srvA, deps, "a builder that is not client-owned takes the session's server too")
	require.Equal(t, int32(2), coreMod.forks.Load(), "built once more after the failure, and only once")
}

// cancelingCoreMod is a core module, identified by content, whose first fork
// blocks until its caller gives up, and fails with the caller's cause.
type cancelingCoreMod struct {
	Mod
	base    *dagql.Server
	forks   atomic.Int32
	entered chan struct{}
}

func (*cancelingCoreMod) Name() string              { return ModuleName }
func (*cancelingCoreMod) View() (call.View, bool)   { return "", false }
func (*cancelingCoreMod) SchemaASTCacheKey() string { return "canceling-core" }
func (m *cancelingCoreMod) ForkSchema(ctx context.Context, root *Query, _ call.View) (*dagql.Server, error) {
	if m.forks.Add(1) == 1 {
		close(m.entered)
		<-ctx.Done()
		return nil, fmt.Errorf("fork: %w", context.Cause(ctx))
	}
	return m.base.Fork(ctx, root)
}

// The first client to load a shared module set builds it with its own
// context. If that client gives up mid-build, a client waiting on the same
// build gets a working server, not the first one's cancellation: the canceled
// build is not kept, so the waiter builds the set itself. Later loads share
// that server.
func TestSchemaBuilderSharedBuildOwnerCanceled(t *testing.T) {
	errGaveUp := errors.New("client a gave up")
	for name, withCancel := range map[string]func(context.Context) (context.Context, func()){
		"cancel": func(ctx context.Context) (context.Context, func()) {
			return context.WithCancel(ctx)
		},
		"cause": func(ctx context.Context) (context.Context, func()) {
			ctx, cancel := context.WithCancelCause(ctx)
			return ctx, func() { cancel(errGaveUp) }
		},
	} {
		t.Run(name, func(t *testing.T) {
			session := NewSchemaBuilderMemo()
			facade := &memoTestServer{mockServer: &mockServer{}, client: NewSchemaBuilderMemo(), session: session}
			base, err := dagql.NewServer(t.Context(), &Query{})
			require.NoError(t, err)
			coreMod := &cancelingCoreMod{base: base, entered: make(chan struct{})}
			client := func() (context.Context, *SchemaBuilder) {
				root := &Query{Server: facade}
				return ContextWithQuery(t.Context(), root), NewSchemaBuilder(root, []Mod{coreMod}).ClientOwned()
			}
			ctxA, servedA := client()
			ctxB, servedB := client()
			ctxA, cancelA := withCancel(ctxA)
			defer cancelA()

			failedA := make(chan error, 1)
			go func() {
				_, err := servedA.Schema(ctxA)
				failedA <- err
			}()
			<-coreMod.entered
			type loaded struct {
				srv *dagql.Server
				err error
			}
			waitedB := make(chan loaded, 1)
			go func() {
				srv, err := servedB.Schema(ctxB)
				waitedB <- loaded{srv, err}
			}()
			time.Sleep(50 * time.Millisecond) // let b wait on a's build
			cancelA()

			errA := <-failedA
			require.ErrorIs(t, errA, context.Cause(ctxA), "a gets its own cancellation")
			b := <-waitedB
			require.NoError(t, b.err, "b does not get a's cancellation")
			require.NotNil(t, b.srv)

			later, err := servedA.Schema(ContextWithQuery(t.Context(), ctxA.Value(queryKey{}).(*Query)))
			require.NoError(t, err, "a's later load succeeds")
			require.Same(t, b.srv, later, "and shares the server b built")
			deps, err := NewSchemaBuilder(&Query{Server: facade}, []Mod{coreMod}).Schema(ctxB)
			require.NoError(t, err)
			require.Same(t, b.srv, deps)
			require.Equal(t, int32(2), coreMod.forks.Load(), "the canceled build, then b's, and no other")
		})
	}
}

// With no session to hold it, the server is built for the one call.
func TestSchemaBuilderWithoutClientBuildsUncached(t *testing.T) {
	deps := NewSchemaBuilder(&Query{}, []Mod{&ctxInstallMod{}})
	srv, err := deps.Schema(t.Context())
	require.NoError(t, err)
	again, err := deps.Schema(t.Context())
	require.NoError(t, err)
	require.NotSame(t, srv, again)
	require.Nil(t, deps.lazilyLoadedServer)
}

// The memo owns a copy, never the caller's builder, and ownership is never
// inherited by a derived builder.
func TestSchemaBuilderMemoOwnsACopy(t *testing.T) {
	deps := NewSchemaBuilder(&Query{}, []Mod{&memoTestMod{name: "a"}})
	owned := NewSchemaBuilderMemo().Get(deps)
	require.NotSame(t, deps, owned)
	require.True(t, owned.clientOwned)
	require.False(t, deps.clientOwned)
	for name, derived := range map[string]*SchemaBuilder{
		"Clone":    owned.Clone(),
		"WithRoot": owned.WithRoot(&Query{}),
		"Append":   owned.Append(&memoTestMod{name: "b"}),
		"Prepend":  owned.Prepend(&memoTestMod{name: "b"}),
		"With":     owned.With(&memoTestMod{name: "b"}, InstallOpts{}),
	} {
		require.False(t, derived.clientOwned, name)
	}
}

// A value workspace's installed modules are loaded once per session, failures
// included; a failed load is tried again.
func TestSchemaBuilderMemoWorkspaceModules(t *testing.T) {
	memo := NewSchemaBuilderMemo()
	loads := 0
	loaded := &InstalledWorkspaceModules{Failures: []ModuleLoadFailure{{Name: "broken"}}}
	load := func() (*InstalledWorkspaceModules, error) {
		loads++
		if loads == 1 {
			return nil, errors.New("canceled")
		}
		return loaded, nil
	}
	_, err := memo.WorkspaceModules("ws", load)
	require.Error(t, err)
	got, err := memo.WorkspaceModules("ws", load)
	require.NoError(t, err)
	require.Same(t, loaded, got)
	got, err = memo.WorkspaceModules("ws", load)
	require.NoError(t, err)
	require.Same(t, loaded, got)
	require.Equal(t, 2, loads)

	var none *SchemaBuilderMemo
	_, err = none.WorkspaceModules("ws", load)
	require.NoError(t, err)
	require.Equal(t, 3, loads, "a nil memo loads every time")
}

// A memo keeps at most maxMemoizedSchemaBuilders builders, evicting the least
// recently used. A caller holding an evicted builder keeps its server: the
// memo only stops sharing it, and the next load of that set builds anew.
func TestSchemaBuilderMemoBounded(t *testing.T) {
	session := NewSchemaBuilderMemo()
	ctx := ContextWithQuery(t.Context(), &Query{Server: &memoTestServer{mockServer: &mockServer{}, client: NewSchemaBuilderMemo(), session: session}})
	deps := func(root *Query) *SchemaBuilder { return NewSchemaBuilder(root, []Mod{&keyedInstallMod{key: "core"}}) }

	firstRoot, recentRoot := &Query{}, &Query{}
	first := session.Get(deps(firstRoot))
	held, err := first.Schema(ctx)
	require.NoError(t, err)
	recent := session.Get(deps(recentRoot))
	for range maxMemoizedSchemaBuilders - 2 {
		session.Get(deps(&Query{}))
	}
	require.Equal(t, maxMemoizedSchemaBuilders, session.builders.Len())
	require.Same(t, first, session.Get(deps(firstRoot)), "the memo is full, not over")

	session.Get(deps(&Query{})) // evicts recent: first was just used
	require.Equal(t, maxMemoizedSchemaBuilders, session.builders.Len(), "the memo is bounded")
	require.Same(t, first, session.Get(deps(firstRoot)), "a recently used builder stays")
	require.NotSame(t, recent, session.Get(deps(recentRoot)), "the least recently used builder was evicted")

	for range maxMemoizedSchemaBuilders {
		session.Get(deps(&Query{}))
	}
	evicted := session.Get(deps(firstRoot))
	require.NotSame(t, first, evicted, "first was evicted in turn")
	again, err := first.Schema(ctx)
	require.NoError(t, err)
	require.Same(t, held, again, "a caller holding an evicted builder keeps its server")
	rebuilt, err := evicted.Schema(ctx)
	require.NoError(t, err)
	require.NotSame(t, held, rebuilt, "the set is built again on its next load")
}

// Every side table of a memo is bounded like its builders, and an entry
// evicted while its caller fills it is still filled for that caller.
func TestSchemaBuilderMemoSideTablesBounded(t *testing.T) {
	memo := NewSchemaBuilderMemo()
	for i := range maxMemoizedGitClients + 10 {
		declared, err := memo.declaredGitClients(declaredGitClientsKey{treeID: uint64(i + 1)}, func() ([]string, error) {
			return []string{"github.com/example/repo"}, nil
		})
		require.NoError(t, err)
		require.Equal(t, []string{"github.com/example/repo"}, declared)
	}
	require.Equal(t, maxMemoizedGitClients, memo.gitClients.Len())

	for i := range maxMemoizedTypes + 10 {
		_, err := memo.canonicalArgType(strconv.Itoa(i), func() (*TypeDef, error) { return &TypeDef{Kind: TypeDefKindString}, nil })
		require.NoError(t, err)
	}
	require.Equal(t, maxMemoizedTypes, memo.canonicalArgTypes.Len())

	for i := range maxMemoizedModuleServers + 10 {
		_, err := memo.moduleServer(strconv.Itoa(i), func() (*dagql.Server, error) { return dagql.NewServer(t.Context(), &Query{}) })
		require.NoError(t, err)
	}
	require.Equal(t, maxMemoizedModuleServers, memo.moduleServers.Len())

	for i := range maxMemoizedWorkspaces + 10 {
		_, err := memo.WorkspaceModules(strconv.Itoa(i), func() (*InstalledWorkspaceModules, error) { return &InstalledWorkspaceModules{}, nil })
		require.NoError(t, err)
	}
	require.Equal(t, maxMemoizedWorkspaces, memo.workspaceModules.Len())

	// An entry evicted while its build runs still returns that build.
	built, _ := dagql.NewServer(t.Context(), &Query{})
	srv, err := memo.moduleServer("evicted while building", func() (*dagql.Server, error) {
		for i := range maxMemoizedModuleServers {
			_, err := memo.moduleServer("filler-"+strconv.Itoa(i), func() (*dagql.Server, error) { return dagql.NewServer(t.Context(), &Query{}) })
			require.NoError(t, err)
		}
		return built, nil
	})
	require.NoError(t, err)
	require.Same(t, built, srv)
	_, ok := memo.moduleServers.Peek("evicted while building")
	require.False(t, ok)
}

func TestSchemaBuilderWithCoreMod(t *testing.T) {
	oldCore := &memoTestMod{name: ModuleName}
	newCore := &memoTestMod{name: ModuleName}
	dependency := &memoTestMod{name: "dependency"}
	root := &Query{}
	original := NewSchemaBuilder(root, []Mod{oldCore}).With(dependency, InstallOpts{Main: true, SkipConstructor: true})
	replaced := original.WithCoreMod(newCore)
	require.Same(t, root, replaced.root)
	require.Equal(t, []Mod{newCore, dependency}, replaced.Mods())
	require.Equal(t, original.entries[1], replaced.entries[1])
	require.Equal(t, []Mod{oldCore, dependency}, original.Mods())
}

// A conversation's Dang scope serves the modules its workspace installs as of
// the call: a module installed since is added, a changed one replaced, an
// uninstalled one dropped, and an unchanged one kept as it was, so the schema
// built for it is reused.
func TestSchemaBuilderWithPrimaryMods(t *testing.T) {
	core := &memoTestMod{name: ModuleName}
	served := &memoTestMod{name: "served"}
	other := &memoTestMod{name: "other"}
	typesOnly := &memoTestMod{name: "types-only"}
	promoted := &memoTestMod{name: "promoted"}
	root := &Query{}
	base := NewSchemaBuilder(root, []Mod{core}).
		With(served, InstallOpts{Main: true}).
		With(other, InstallOpts{}).
		With(typesOnly, InstallOpts{SkipConstructor: true}).
		With(promoted, InstallOpts{SkipConstructor: true})

	sameServed := &memoTestMod{name: "served", same: served}
	promotedAgain := &memoTestMod{name: "promoted", same: promoted}
	installed := &memoTestMod{name: "installed"}
	got := base.WithPrimaryMods([]Mod{installed, sameServed, promotedAgain}, nil)

	require.Same(t, root, got.root)
	require.Equal(t, []Mod{core, served, other, typesOnly, promoted, installed}, got.Mods(),
		"a new module is appended")
	require.Same(t, served, got.Mods()[1], "the same served module is kept as it was")
	require.Same(t, promoted, got.Mods()[4])
	require.Equal(t, []Mod{core, served, other, promoted, installed}, got.PrimaryMods(),
		"a module served only for its types gets its constructor")
	opts, _ := got.InstallOpts("served")
	require.True(t, opts.Main, "a kept entry keeps its install options")
	require.Len(t, base.Mods(), 5, "the builder itself is unchanged")

	changed := &memoTestMod{name: "served"}
	replaced := base.WithPrimaryMods([]Mod{changed}, nil)
	require.Same(t, changed, replaced.Mods()[1], "a different module of the same name replaces the served one")
	opts, _ = replaced.InstallOpts("served")
	require.True(t, opts.Main, "a replaced entry keeps its install options")
	require.Equal(t, base.Mods(), base.WithPrimaryMods(nil, nil).Mods())
	require.Same(t, base, base.WithPrimaryMods([]Mod{sameServed, other}, []string{"types-only"}),
		"a builder nothing changes is kept, with its server")

	// A module the workspace uninstalled is dropped; a module served only
	// for its types, which no workspace installs, is kept.
	require.Equal(t, []Mod{core, served, typesOnly, promoted},
		base.WithPrimaryMods(nil, []string{"other", "types-only"}).Mods())
}
