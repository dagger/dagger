package core

import (
	"context"
	"errors"
	"testing"

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
