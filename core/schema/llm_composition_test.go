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

type compositionTestServer struct {
	*currentTypeDefsTestServer
	module    dagql.ObjectResult[*core.Module]
	moduleErr error
}

func (s *compositionTestServer) CurrentModule(context.Context) (dagql.ObjectResult[*core.Module], error) {
	return s.module, s.moduleErr
}

func TestLLMCompositionOwnerSelectorsRoundTrip(t *testing.T) {
	md := &engine.ClientMetadata{ClientID: "composition-client", SessionID: "composition-session"}
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

	var tool dagql.ObjectResult[*core.TypeDef]
	require.NoError(t, srv.Select(ctx, srv.Root(), &tool, dagql.Selector{Field: "typeDef"}))
	toolID, err := tool.ID()
	require.NoError(t, err)
	var dir dagql.ObjectResult[*core.Directory]
	require.NoError(t, srv.Select(ctx, srv.Root(), &dir, dagql.Selector{Field: "directory"}))
	dirID, err := dir.ID()
	require.NoError(t, err)
	var seed dagql.ObjectResult[*core.LLM]
	require.NoError(t, srv.Select(ctx, srv.Root(), &seed,
		dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}},
		dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("same prompt")}}},
	))
	setCaller := func(name string) {
		t.Helper()
		server.module = dagql.ObjectResult[*core.Module]{}
		if name != "" {
			mod := &core.Module{NameField: name, OriginalName: "intrinsic-name"}
			server.module, err = dagql.NewObjectResultForCall(mod, srv, implementationScopedTestSyntheticCall("caller-"+name, mod))
			require.NoError(t, err)
		}
	}
	for _, test := range []struct {
		name   string
		caller string
		owner  dagql.Optional[dagql.String]
		want   string
	}{
		{"main client is unowned", "", dagql.Optional[dagql.String]{}, ""},
		{"omitted uses caller A", "group-A", dagql.Optional[dagql.String]{}, "group-A"},
		{"omitted uses caller B", "group-B", dagql.Optional[dagql.String]{}, "group-B"},
		{"explicit empty is unowned", "group-A", dagql.Opt(dagql.String("")), ""},
		{"explicit other owner", "group-A", dagql.Opt(dagql.String("group-B")), "group-B"},
		{"owner prefix collision", "group-A", dagql.Opt(dagql.String("group-AB")), "group-AB"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setCaller(test.caller)
			promptArgs := []dagql.NamedInput{{Name: "prompt", Value: dagql.String("same prompt")}}
			toolArgs := []dagql.NamedInput{{Name: "object", Value: dagql.NewAnyID(toolID)}}
			skillArgs := []dagql.NamedInput{{Name: "directory", Value: dagql.NewID[*core.Directory](dirID)}}
			if test.owner.Valid {
				promptArgs = append(promptArgs, dagql.NamedInput{Name: "owner", Value: test.owner})
				toolArgs = append(toolArgs, dagql.NamedInput{Name: "owner", Value: test.owner})
				skillArgs = append(skillArgs, dagql.NamedInput{Name: "owner", Value: test.owner})
			}
			// Each call uses the same receiver and inputs across callers, so a
			// cache key missing the owner would return another caller's stamp.
			for _, sel := range []dagql.Selector{
				{Field: "withSystemPrompt", Args: promptArgs},
				{Field: "withTools", Args: toolArgs},
				{Field: "withSkills", Args: skillArgs},
			} {
				var result dagql.ObjectResult[*core.LLM]
				require.NoError(t, srv.Select(ctx, seed, &result, sel))
				id, err := result.RecipeID(ctx)
				require.NoError(t, err)
				require.NotNil(t, id.Arg("owner"))
				require.Equal(t, test.want, id.Arg("owner").Value().ToInput())
			}
			var llm dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, seed, &llm,
				dagql.Selector{Field: "withSystemPrompt", Args: promptArgs},
				dagql.Selector{Field: "withTools", Args: toolArgs},
				dagql.Selector{Field: "withSkills", Args: skillArgs},
			))
			require.Empty(t, llm.Self().Messages[0].CompositionOwner)
			require.Equal(t, test.want, llm.Self().Messages[1].CompositionOwner)

			setCaller("replay-caller")
			portable, err := llm.Self().PortableRecipe(ctx)
			require.NoError(t, err)
			require.Equal(t, test.want, portable.Self().Messages[1].CompositionOwner)
			recipe, err := portable.RecipeID(ctx)
			require.NoError(t, err)
			encoded, err := recipe.Encode()
			require.NoError(t, err)
			var decoded call.ID
			require.NoError(t, decoded.Decode(encoded))
			require.Equal(t, []string{test.want}, compositionRecipeToolOwners(t, &decoded))
			require.Equal(t, []string{test.want}, compositionRecipeOwners(t, &decoded, "withSkills"))

			var removed dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, portable, &removed, dagql.Selector{
				Field: "__withoutComposition", Args: []dagql.NamedInput{{Name: "owner", Value: dagql.String("group-A")}},
			}))
			removedPortable, err := removed.Self().PortableRecipe(ctx)
			require.NoError(t, err)
			removedRecipe, err := removedPortable.RecipeID(ctx)
			require.NoError(t, err)
			if test.want == "group-A" {
				require.Len(t, removedPortable.Self().Messages, 1)
				require.Empty(t, compositionRecipeToolOwners(t, removedRecipe))
				require.Empty(t, compositionRecipeOwners(t, removedRecipe, "withSkills"))
			} else {
				require.Len(t, removedPortable.Self().Messages, 2)
				require.Equal(t, []string{test.want}, compositionRecipeToolOwners(t, removedRecipe))
				require.Equal(t, []string{test.want}, compositionRecipeOwners(t, removedRecipe, "withSkills"))
			}
		})
	}

	// The explicit owner override is marked as engine-internal replay plumbing
	// so author SDKs and tool schemas do not offer it as a normal argument.
	llmType, ok := srv.ObjectType("LLM")
	require.True(t, ok)
	for _, view := range []call.View{"v0.19.0", "v1.0.0"} {
		_, ok := llmType.FieldSpec("__withCompositionOwner", view)
		require.False(t, ok)
	}
	_, ok = llmType.FieldSpec("__withoutComposition", "v0.19.0")
	require.False(t, ok)
	_, ok = llmType.FieldSpec("__withoutComposition", "v1.0.0")
	require.True(t, ok)
	setCaller("")
	// No-current-module is the normal main-client case; other errors must not
	// silently turn a module's contributions into unowned state.
	for _, moduleErr := range []error{core.ErrNoCurrentModule, fmt.Errorf("caller lookup failed")} {
		server.moduleErr = moduleErr
		var result dagql.ObjectResult[*core.LLM]
		err := srv.Select(ctx, seed, &result, dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("lookup")}}})
		if moduleErr == core.ErrNoCurrentModule {
			require.NoError(t, err)
			require.Empty(t, result.Self().Messages[1].CompositionOwner)
		} else {
			require.ErrorContains(t, err, moduleErr.Error())
		}
	}
	server.moduleErr = nil
	// Default and explicit versions survive reconstruction of a portable recipe.
	for _, version := range []int{1, 0, 7} {
		t.Run(fmt.Sprintf("binding version %d", version), func(t *testing.T) {
			toolArgs := []dagql.NamedInput{{Name: "object", Value: dagql.NewAnyID(toolID)}}
			if version != 0 {
				toolArgs = append(toolArgs, dagql.NamedInput{Name: "version", Value: dagql.Int(version)})
			}
			var llm dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, srv.Root(), &llm,
				dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}},
				dagql.Selector{Field: "withTools", Args: toolArgs},
			))
			portable, err := llm.Self().PortableRecipe(ctx)
			require.NoError(t, err)
			recipe, err := portable.RecipeID(ctx)
			require.NoError(t, err)
			found := false
			for id := recipe; id != nil; id = id.Receiver() {
				if id.Field() != "withTools" {
					continue
				}
				found = true
				arg := id.Arg("version")
				require.NotNil(t, arg)
				require.EqualValues(t, version, arg.Value().ToInput())
			}
			require.True(t, found)
		})
	}

	for _, name := range []string{"withTools", "withSystemPrompt", "withSkills"} {
		field, ok := llmType.FieldSpec(name, srv.View)
		require.True(t, ok)
		owner, ok := field.Args.Input("owner", srv.View)
		require.True(t, ok)
		require.True(t, owner.Internal)
		require.NotNil(t, field.FieldDefinition(srv.View).Arguments.ForName("owner").Directives.ForName("internal"))
	}
}

func compositionRecipeToolOwners(t *testing.T, recipe *call.ID) []string {
	t.Helper()
	return compositionRecipeOwners(t, recipe, "withTools")
}

func compositionRecipeOwners(t *testing.T, recipe *call.ID, field string) []string {
	t.Helper()
	var owners []string
	for id := recipe; id != nil; id = id.Receiver() {
		if id.Field() == field {
			arg := id.Arg("owner")
			require.NotNil(t, arg)
			owner, ok := arg.Value().ToInput().(string)
			require.True(t, ok)
			owners = append(owners, owner)
		}
	}
	return owners
}
