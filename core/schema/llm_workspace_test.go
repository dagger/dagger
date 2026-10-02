package schema

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// TestLLMRestoreLoadsWorkspaceLazily covers LLM.withWorkspace's LazyRef
// argument: loading a conversation from its recipe must not evaluate the
// workspaces it was bound to — a long agent session binds dozens, each with its
// own history of commits, pulls and generated changes — and the first use of
// the workspace loads only the latest binding.
func TestLLMRestoreLoadsWorkspaceLazily(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, sameSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("warm=%t/sameSession=%t", warm, sameSession), func(t *testing.T) {
				md := &engine.ClientMetadata{ClientID: "source", SessionID: "source"}
				ctx := engine.ContextWithClientMetadata(t.Context(), md)
				cache, err := dagql.NewCache(ctx, "", nil, nil)
				require.NoError(t, err)
				ctx = dagql.ContextWithCache(ctx, cache)
				server := &compositionTestServer{currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: md}}
				root := core.NewRoot(server)
				ctx = core.ContextWithQuery(ctx, root)
				base, err := NewCoreSchemaBase(ctx, server)
				require.NoError(t, err)
				srv, err := base.Fork(ctx, root, "")
				require.NoError(t, err)
				server.dag = srv

				// expensiveWorkspace stands in for a workspace with a costly or
				// side-effecting history (commits, pulls, generator execs). It
				// counts its evaluations, and fails for superseded bindings once
				// the conversation has been recorded.
				calls := map[string]int{}
				fail := false
				dagql.Fields[*core.Query]{
					dagql.Func("expensiveWorkspace", func(_ context.Context, _ *core.Query, args struct{ Name string }) (*core.Workspace, error) {
						calls[args.Name]++
						if fail && args.Name != "latest" {
							return nil, fmt.Errorf("superseded workspace %q must not be evaluated", args.Name)
						}
						return &core.Workspace{Cwd: args.Name}, nil
					}),
				}.Install(srv)

				var seed dagql.ObjectResult[*core.LLM]
				require.NoError(t, srv.Select(ctx, srv.Root(), &seed, dagql.Selector{
					Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}},
				}))
				// Record a conversation the way a live client builds one: each
				// workspace is evaluated and bound by its handle.
				llm := seed
				wsRecipe := func(name string) *call.ID {
					return call.New().Append((&core.Workspace{}).Type(), "expensiveWorkspace",
						call.WithArgs(call.NewArgument("name", call.NewLiteralString(name), false)))
				}
				for _, name := range []string{"first", "second", "latest"} {
					ws, err := srv.Load(ctx, wsRecipe(name))
					require.NoError(t, err)
					handle, err := ws.ID()
					require.NoError(t, err)
					require.True(t, handle.IsHandle())
					require.NoError(t, srv.Select(ctx, llm, &llm,
						dagql.Selector{Field: "withWorkspace", Args: []dagql.NamedInput{
							{Name: "workspace", Value: dagql.NewID[*core.Workspace](handle)},
						}},
						dagql.Selector{Field: "withPrompt", Args: []dagql.NamedInput{
							{Name: "prompt", Value: dagql.String("now in " + name)},
						}},
					))
				}
				recipe, err := llm.RecipeID(ctx)
				require.NoError(t, err)

				calls, fail = map[string]int{}, true
				if !sameSession {
					ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "destination", SessionID: "destination"})
				}
				if !warm {
					cache, err = dagql.NewCache(ctx, "", nil, nil)
					require.NoError(t, err)
					ctx = dagql.ContextWithCache(ctx, cache)
				}
				loaded, err := srv.Load(ctx, recipe)
				require.NoError(t, err)
				require.Empty(t, calls, "loading the conversation must not evaluate any bound workspace")

				restored, ok := loaded.(dagql.ObjectResult[*core.LLM])
				require.True(t, ok)
				restoredRecipe, err := restored.RecipeID(ctx)
				require.NoError(t, err)
				require.Equal(t, workspaceArgDigests(t, recipe), workspaceArgDigests(t, restoredRecipe),
					"a lazy workspace must keep its binding's identity")
				if sameSession {
					// Query.llm is per-session, so only a same-session restore
					// reproduces the whole digest.
					require.Equal(t, recipe.Digest(), restoredRecipe.Digest())
				}

				// Presence is answered without loading.
				require.True(t, restored.Self().HasWorkspace())
				require.Empty(t, calls)

				// Using the workspace loads the latest binding, once.
				var ws dagql.ObjectResult[*core.Workspace]
				require.NoError(t, srv.Select(ctx, restored, &ws, dagql.Selector{Field: "workspace"}))
				require.Equal(t, "latest", ws.Self().Cwd)
				direct, err := restored.Self().Workspace(ctx)
				require.NoError(t, err)
				require.Equal(t, "latest", direct.Self().Cwd)
				require.Zero(t, calls["first"])
				require.Zero(t, calls["second"])
				if warm {
					require.Zero(t, calls["latest"], "a warm workspace is a cache hit")
				} else {
					require.Equal(t, 1, calls["latest"])
				}
			})
		}
	}
}

// workspaceArgDigests lists the recipe digests of a conversation's
// withWorkspace bindings, latest first.
func workspaceArgDigests(t *testing.T, recipe *call.ID) []string {
	t.Helper()
	var digests []string
	for id := recipe; id != nil; id = id.Receiver() {
		if id.Field() != "withWorkspace" {
			continue
		}
		arg := id.Arg("workspace")
		require.NotNil(t, arg)
		lit, ok := arg.Value().(*call.LiteralID)
		require.True(t, ok, "workspace arg is %T", arg.Value())
		require.False(t, lit.Value().IsHandle())
		digests = append(digests, lit.Value().Digest().String())
	}
	require.Len(t, digests, 3)
	return digests
}
