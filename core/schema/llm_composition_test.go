package schema

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestLLMCompositionOwnerSelectorsRoundTrip(t *testing.T) {
	md := &engine.ClientMetadata{ClientID: "composition-client", SessionID: "composition-session"}
	ctx := engine.ContextWithClientMetadata(t.Context(), md)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	server := &currentTypeDefsTestServer{mainClient: md}
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
	for _, test := range []struct {
		name  string
		owner dagql.Optional[dagql.String]
		want  string
	}{
		{"omitted inherits scope", dagql.Optional[dagql.String]{}, "group-A"},
		{"explicit empty is unowned", dagql.Opt(dagql.String("")), ""},
		{"explicit other owner", dagql.Opt(dagql.String("group-B")), "group-B"},
	} {
		t.Run(test.name, func(t *testing.T) {
			promptArgs := []dagql.NamedInput{{Name: "prompt", Value: dagql.String("same prompt")}}
			toolArgs := []dagql.NamedInput{{Name: "object", Value: dagql.NewAnyID(toolID)}}
			if test.owner.Valid {
				promptArgs = append(promptArgs, dagql.NamedInput{Name: "owner", Value: test.owner})
				toolArgs = append(toolArgs, dagql.NamedInput{Name: "owner", Value: test.owner})
			}
			var llm dagql.ObjectResult[*core.LLM]
			require.NoError(t, srv.Select(ctx, srv.Root(), &llm,
				dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.String("test-model"))}}},
				dagql.Selector{Field: "withSystemPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.String("same prompt")}}},
				dagql.Selector{Field: "__withCompositionOwner", Args: []dagql.NamedInput{{Name: "owner", Value: dagql.String("group-A")}}},
				dagql.Selector{Field: "withSystemPrompt", Args: promptArgs},
				dagql.Selector{Field: "withTools", Args: toolArgs},
			))
			require.Equal(t, "group-A", llm.Self().CompositionOwner(), "explicit stamp must not change active scope")
			require.Empty(t, llm.Self().Messages[0].CompositionOwner)
			require.Equal(t, test.want, llm.Self().Messages[1].CompositionOwner)

			portable, err := llm.Self().PortableRecipe(ctx)
			require.NoError(t, err)
			require.Equal(t, "group-A", portable.Self().CompositionOwner())
			require.Equal(t, test.want, portable.Self().Messages[1].CompositionOwner)
			recipe, err := portable.RecipeID(ctx)
			require.NoError(t, err)
			encoded, err := recipe.Encode()
			require.NoError(t, err)
			var decoded call.ID
			require.NoError(t, decoded.Decode(encoded))
			require.Equal(t, []string{test.want}, compositionRecipeToolOwners(t, &decoded))

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
			} else {
				require.Len(t, removedPortable.Self().Messages, 2)
				require.Equal(t, []string{test.want}, compositionRecipeToolOwners(t, removedRecipe))
			}
			require.Equal(t, "group-A", removedPortable.Self().CompositionOwner())
		})
	}

	// The explicit owner override is marked as engine-internal replay plumbing
	// so author SDKs and tool schemas do not offer it as a normal argument.
	llmType, ok := srv.ObjectType("LLM")
	require.True(t, ok)
	for _, name := range []string{"__withCompositionOwner", "__withoutComposition"} {
		_, ok := llmType.FieldSpec(name, "v0.19.0")
		require.False(t, ok)
		_, ok = llmType.FieldSpec(name, "v1.0.0")
		require.True(t, ok)
	}
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

	for _, name := range []string{"withTools", "withSystemPrompt"} {
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
	var owners []string
	for id := recipe; id != nil; id = id.Receiver() {
		if id.Field() == "withTools" {
			arg := id.Arg("owner")
			require.NotNil(t, arg)
			owner, ok := arg.Value().ToInput().(string)
			require.True(t, ok)
			owners = append(owners, owner)
		}
	}
	return owners
}
