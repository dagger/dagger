package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/stretchr/testify/require"
)

func TestAgentCaptureValidatesLazyAndImplicitDependencies(t *testing.T) {
	srv, err := dagql.NewServer(t.Context(), &Query{})
	require.NoError(t, err)
	dagql.Fields[*Query]{
		dagql.Func("unsafeCapture", func(context.Context, *Query, struct{}) (dagql.String, error) {
			t.Fatal("validation must never execute a constructor")
			return "", nil
		}).NotReplayable("source client required"),
		dagql.Func("lazyCapture", func(context.Context, *Query, struct{ Object dagql.AnyID }) (dagql.String, error) {
			t.Fatal("validation must never execute a constructor")
			return "", nil
		}).Args(dagql.Arg("object").LazyRef()),
	}.Install(srv)
	typ := dagql.String("").Type()
	unsafe := call.New().Append(typ, "unsafeCapture")
	lazy := call.New().Append(typ, "lazyCapture", call.WithArgs(call.NewArgument("object", call.NewLiteralID(unsafe), false)))
	require.Nil(t, srv.ClassifyRecipe(lazy).NotReplayable, "ordinary loading deliberately skips a lazy object")
	require.ErrorContains(t, validateAgentRecipe(srv, lazy), "source client required", "capture must retain safe future-dispatch dependencies too")
	implicit := call.New().Append(typ, "other", call.WithImplicitInputs(call.NewArgument("dependency", call.NewLiteralID(unsafe), false)))
	require.ErrorContains(t, validateAgentRecipe(srv, implicit), "source client required")
	nested := call.New().Append(typ, "other", call.WithArgs(call.NewArgument("nested", call.NewLiteralList(call.NewLiteralObject(call.NewArgument("value", call.NewLiteralID(unsafe), false))), false)))
	require.ErrorContains(t, validateAgentRecipe(srv, nested), "source client required")
	for _, field := range []string{"directory", "file", "unixSocket", "service", "__gitDir"} {
		host := call.New().Append((&Host{}).Type(), "host")
		require.ErrorContains(t, validateAgentRecipe(srv, host.Append(typ, field)), "Host."+field)
	}
	require.NoError(t, validateAgentRecipe(srv, call.New().Append(typ, "blob")), "immutable inline content is not a host dependency")
	require.ErrorContains(t, validateAgentRecipe(srv, call.NewEngineResultID(42, call.NewType(typ))), "engine-local handle")
}

func TestAgentCaptureValidatesOwnedSkillDirectories(t *testing.T) {
	ctx := llmTestContext()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv, err := dagql.NewServer(ctx, &Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass[*Directory](srv))
	srv.InstallObject(dagql.NewClass[*Host](srv))
	var loads int
	dagql.Fields[*Query]{
		dagql.Func("portableSkills", func(context.Context, *Query, struct{}) (*Directory, error) {
			loads++
			return &Directory{}, nil
		}),
		dagql.Func("host", func(context.Context, *Query, struct{}) (*Host, error) { return &Host{}, nil }),
	}.Install(srv)
	dagql.Fields[*Host]{
		dagql.Func("directory", func(context.Context, *Host, struct{}) (*Directory, error) {
			loads++
			return &Directory{}, nil
		}),
	}.Install(srv)
	for _, owner := range []string{"", "module-A"} {
		for _, hostBacked := range []bool{false, true} {
			sels := []dagql.Selector{{Field: "portableSkills"}}
			if hostBacked {
				sels = []dagql.Selector{{Field: "host"}, {Field: "directory"}}
			}
			var directory dagql.ObjectResult[*Directory]
			require.NoError(t, srv.Select(ctx, srv.Root(), &directory, sels...))
			llm := &LLM{mcp: (&MCP{}).withSkillsOwner(directory, owner)}
			before, err := directory.RecipeID(ctx)
			require.NoError(t, err)
			loaded := loads
			err = llm.validateAgentBindings(ctx, srv)
			if hostBacked {
				require.ErrorContains(t, err, "capture skills: agent capture depends on originating client via Host.directory")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, loaded, loads, "validation must not reload skill content")
			require.Equal(t, owner, llm.mcp.skillDirs[0].Owner, "capture must preserve recomposition ownership")
			after, err := llm.mcp.skillDirs[0].Directory.RecipeID(ctx)
			require.NoError(t, err)
			require.Equal(t, before.Digest(), after.Digest())
		}
	}
}

func TestAgentCapturePayloadsCheckEveryDependency(t *testing.T) {
	rec, ctx := payloadRecorderCtx(t)
	typ := dagql.String("").Type()
	dependency := call.New().Append(typ, "dependency")
	root := call.New().Append(typ, "root", call.WithArgs(call.NewArgument("value", call.NewLiteralID(dependency), false)))
	seen := map[string]bool{}
	require.NoError(t, emitAgentCapturePayloads(ctx, root, func(digest string) bool { seen[digest] = true; return digest == root.Digest().String() }))
	require.Len(t, seen, 2, "a durably persisted root does not short-circuit closure traversal")
	require.Equal(t, 1, rec.emissionCount())
	require.NotNil(t, rec.get(dependency.Digest().String()))
	// Merely reported/claimed span frames are not durable: a false persisted
	// predicate emits both frames via canonical protected payload records.
	require.NoError(t, emitAgentCapturePayloads(ctx, root, func(string) bool { return false }))
	require.Equal(t, 3, rec.emissionCount())
	require.NotNil(t, rec.get(root.Digest().String()))
}

func TestAgentCaptureChecksModuleAndNestedDependencies(t *testing.T) {
	srv, err := dagql.NewServer(t.Context(), &Query{})
	require.NoError(t, err)
	// ToProto includes the full dependency DAG, even when a module reference
	// or nested literal is the only route to an unsafe frame. The strict
	// validator visits every frame rather than following only the receiver.
	frames := map[string]*callpbv1.Call{
		"host":  {Field: "host", Type: &callpbv1.Type{NamedType: "Host"}},
		"local": {Field: "directory", ReceiverDigest: "host", Type: &callpbv1.Type{NamedType: "Directory"}},
		"root":  {Field: "tool", Type: &callpbv1.Type{NamedType: "Tool"}, Module: &callpbv1.Module{CallDigest: "local"}},
	}
	require.ErrorContains(t, validateAgentRecipeDAG(srv, &callpbv1.RecipeDAG{RootDigest: "root", CallsByDigest: frames}), "Host.directory")
}
