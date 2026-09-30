package core

import (
	"context"
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
}

func (m *memoTestMod) Name() string { return m.name }

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
		require.NotSame(t, withB, memo.Get(base.With(b, InstallOpts{Entrypoint: true})),
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

// memoTestServer hands out one session's schema builder memo.
type memoTestServer struct {
	*mockServer
	session *SchemaBuilderMemo
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
		return ContextWithQuery(t.Context(), &Query{Server: &memoTestServer{mockServer: &mockServer{}, session: session}})
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
