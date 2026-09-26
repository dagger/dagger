package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestLLMCompositionOwnerPrompts(t *testing.T) {
	base, err := (&Query{}).NewLLM(t.Context(), "test-model", "")
	require.NoError(t, err)
	base = base.WithSystemPrompt("same text").WithPrompt("user history")
	owned := base.WithSystemPromptOwner("same text", "group-A")
	other := owned.WithSystemPromptOwner("same text", "group-B")
	composed := other.WithSystemPromptOwner("outer after nested", "group-A")
	composed = composed.WithResponse([]*LLMContentBlock{{Kind: LLMContentText, Text: "assistant history"}}, LLMTokenUsage{}).
		WithToolResult("call-1", "tool history", false)

	require.Equal(t, "group-A", owned.Messages[2].CompositionOwner)
	require.Nil(t, owned.Messages[2].Origin)
	require.Equal(t, "group-A", owned.Messages[2].Clone().CompositionOwner)
	data, err := json.Marshal(owned.Messages[2])
	require.NoError(t, err)
	var restored LLMMessage
	require.NoError(t, json.Unmarshal(data, &restored))
	require.Equal(t, "group-A", restored.CompositionOwner)

	removed := composed.WithoutComposition("group-A")
	require.Len(t, removed.Messages, 5)
	require.Equal(t, "same text", removed.Messages[0].TextContent())
	require.Empty(t, removed.Messages[0].CompositionOwner)
	require.Equal(t, "group-B", removed.Messages[2].CompositionOwner)
	require.Equal(t, "user history", removed.Messages[1].TextContent())
	require.Equal(t, "assistant history", removed.Messages[3].TextContent())
	require.Equal(t, "tool history", removed.Messages[4].Content[0].Text)
	require.Len(t, composed.Messages, 7, "removal must not mutate the original")
	require.Len(t, composed.WithoutComposition("").Messages, 7, "never remove unowned state")

	// Recomposition replaces exactly the owned prompts, even when another
	// owner and the caller installed textually identical prompts.
	recomposed := removed.WithSystemPromptOwner("updated", "group-A")
	require.Len(t, recomposed.Messages, 6)
	require.Equal(t, "updated", recomposed.Messages[5].TextContent())
	require.Len(t, recomposed.WithoutComposition("group-A").Messages, 5)
}

func TestLLMCompositionOwnerFlatIdentity(t *testing.T) {
	// Module identities are exact, not scope paths or prefixes.
	require.False(t, compositionOwnerMatches("outer\ninner", "outer"))
	require.False(t, compositionOwnerMatches("", ""))
	base, err := (&Query{}).NewLLM(t.Context(), "test-model", "")
	require.NoError(t, err)
	owners := []string{"", "outer", "outer\ninner", "outer\ninner\ngrandchild", "inner", "outerish", "outer\ninnerish", "other\ninner"}
	for _, owner := range owners {
		base = base.WithSystemPromptOwner("same text", owner)
		base.mcp.boundTools = append(base.mcp.boundTools, boundTool{Owner: owner})
	}
	for _, tc := range []struct {
		owner string
		want  []string
	}{
		{"", owners},
		{"outer", []string{"", "outer\ninner", "outer\ninner\ngrandchild", "inner", "outerish", "outer\ninnerish", "other\ninner"}},
		{"outer\ninner", []string{"", "outer", "outer\ninner\ngrandchild", "inner", "outerish", "outer\ninnerish", "other\ninner"}},
		{"outer\ninner\ngrandchild", []string{"", "outer", "outer\ninner", "inner", "outerish", "outer\ninnerish", "other\ninner"}},
		{"inner", []string{"", "outer", "outer\ninner", "outer\ninner\ngrandchild", "outerish", "outer\ninnerish", "other\ninner"}},
	} {
		t.Run(tc.owner, func(t *testing.T) {
			removed := base.WithoutComposition(tc.owner)
			var promptOwners []string
			for _, msg := range removed.Messages {
				promptOwners = append(promptOwners, msg.CompositionOwner)
			}
			require.Equal(t, tc.want, promptOwners)
			require.Equal(t, tc.want, compositionBindingOwners(removed.mcp.boundTools))
			require.Len(t, base.Messages, len(owners), "removal must not mutate the base")
			require.Equal(t, owners, compositionBindingOwners(base.mcp.boundTools))
		})
	}
}

type compositionTestObject struct {
	Name  string
	State string
}

func (obj *compositionTestObject) Type() *ast.Type {
	return &ast.Type{NamedType: obj.Name, NonNull: true}
}

func TestLLMCompositionOwnerBindingsAndReplay(t *testing.T) {
	ctx := llmTestContext()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, LLMTestQuery{})
	makeObject := func(name, state string) dagql.ObjectResult[*compositionTestObject] {
		obj := &compositionTestObject{Name: name, State: state}
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*compositionTestObject]{Typed: obj}))
		return newTypeDefAttachedResult(t, ctx, cache, srv, name+state, obj)
	}
	first := makeObject("First", "initial")
	other := makeObject("Other", "initial")
	manual := makeObject("Manual", "initial")
	base, err := (&Query{}).NewLLM(ctx, "test-model", "")
	require.NoError(t, err)
	base = base.WithTools(manual, srv.Schema(), nil)
	owned := base.WithToolsOwner(first, srv.Schema(), []string{"hidden"}, "group-A", 7)
	otherID, err := other.ID()
	require.NoError(t, err)
	owned = owned.WithLazyToolsOwner(otherID, other.ObjectType(), srv.Schema(), nil, "group-B", 3)
	require.Equal(t, []string{"", "group-A", "group-B"}, compositionBindingOwners(owned.mcp.boundTools))

	before, err := owned.mcp.BoundToolBindings()
	require.NoError(t, err)
	next := owned.Clone()
	updated := makeObject("First", "updated")
	require.NoError(t, next.mcp.rebindBoundTool("First", updated))
	require.Equal(t, "group-A", next.mcp.boundTools[1].Owner)
	require.Equal(t, 7, next.mcp.boundTools[1].Version)
	require.Equal(t, 0, next.mcp.boundTools[0].Version, "omitting version defaults to 0")
	require.Equal(t, "initial", owned.mcp.boundTools[1].object.Unwrap().(*compositionTestObject).State)
	delta := stateDeltaSelectors(next.mcp, nil, before)
	require.Len(t, delta, 1)
	require.Equal(t, "withTools", delta[0].Field)
	require.Equal(t, "group-A", compositionSelectorOwner(t, delta[0]))
	require.Equal(t, 7, compositionSelectorVersion(t, delta[0]))
	versionOnly := next.WithToolsOwner(updated, srv.Schema(), []string{"hidden"}, "group-A", 8)
	versionDelta := stateDeltaSelectors(versionOnly.mcp, nil, mustCompositionBindings(t, next.mcp))
	require.Len(t, versionDelta, 1, "a version-only change must be recorded")
	require.Equal(t, 8, compositionSelectorVersion(t, versionDelta[0]))
	require.Equal(t, []string{"", "group-B"}, compositionBindingOwners(next.WithoutComposition("group-A").mcp.boundTools))

	// Explicit rebinding outside a composition belongs to the caller, rather
	// than inheriting the old same-type binding's owner.
	manualRebind := next.WithTools(updated, srv.Schema(), []string{"hidden"})
	require.Empty(t, manualRebind.mcp.boundTools[1].Owner)
	require.Len(t, manualRebind.WithoutComposition("group-A").mcp.boundTools, 3)
	changedOwnerDelta := stateDeltaSelectors(manualRebind.mcp, nil, mustCompositionBindings(t, next.mcp))
	require.Len(t, changedOwnerDelta, 1)
	require.Empty(t, compositionSelectorOwner(t, changedOwnerDelta[0]))

	srv.InstallObject(dagql.NewClass[*LLM](srv))
	// Recomposition must not transfer bindings between distinct module owners,
	// regardless of whether both modules are being recomposed.
	for _, tc := range []struct {
		name, previous, candidate string
		conflict                  bool
	}{
		{"different modules", "inner", "outer", true},
		{"prefix collision", "outerish", "outer", true},
		{"no subtree identity", "outer\ninner", "outer", true},
		{"same module", "outer", "outer", false},
		{"unowned migration", "", "outer", false},
		{"unowned retained", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := newTypeDefDetachedResult(t, srv, "previous-"+tc.name,
				base.WithToolsOwner(first, srv.Schema(), nil, tc.previous, 0))
			candidate := newTypeDefDetachedResult(t, srv, "candidate-"+tc.name,
				base.WithToolsOwner(first, srv.Schema(), nil, tc.candidate, 0))
			_, err := preserveRecomposedTools(ctx, srv, previous, candidate)
			if tc.conflict {
				require.ErrorContains(t, err, "owned by other expertise")
			} else {
				require.NoError(t, err)
			}
		})
	}

	// Recipe recording retains final binding owners and each prompt's owner.
	// Explicit empty owners must also be recorded, not inferred during replay.
	scoped := next.WithSystemPrompt("manual").WithSystemPromptOwner("owned", "group-A")
	sels, err := scoped.recipeSelectors(context.Background())
	require.NoError(t, err)
	var toolVersions []int
	var toolOwners, promptOwners []string
	for _, sel := range sels {
		switch sel.Field {
		case "withTools":
			toolVersions = append(toolVersions, compositionSelectorVersion(t, sel))
			toolOwners = append(toolOwners, compositionSelectorOwner(t, sel))
		case "withSystemPrompt":
			promptOwners = append(promptOwners, compositionSelectorOwner(t, sel))
		}
	}
	require.Equal(t, []int{0, 7, 3}, toolVersions)
	require.Equal(t, []string{"", "group-A", "group-B"}, toolOwners)
	require.Equal(t, []string{"", "group-A"}, promptOwners)
	for _, sel := range sels {
		require.NotEqual(t, "__withCompositionOwner", sel.Field)
	}
}

func compositionBindingOwners(bindings []boundTool) []string {
	owners := make([]string, len(bindings))
	for i, binding := range bindings {
		owners[i] = binding.Owner
	}
	return owners
}

func mustCompositionBindings(t *testing.T, m *MCP) []boundToolBinding {
	t.Helper()
	bindings, err := m.BoundToolBindings()
	require.NoError(t, err)
	return bindings
}

func compositionSelectorVersion(t *testing.T, sel dagql.Selector) int {
	t.Helper()
	for _, arg := range sel.Args {
		if arg.Name == "version" {
			version, ok := arg.Value.(dagql.Int)
			require.True(t, ok)
			return int(version)
		}
	}
	t.Fatalf("selector %s has no version", sel.Field)
	return 0
}

func compositionSelectorOwner(t *testing.T, sel dagql.Selector) string {
	t.Helper()
	for _, arg := range sel.Args {
		if arg.Name == "owner" {
			owner, ok := arg.Value.(dagql.Optional[dagql.String])
			require.True(t, ok)
			require.True(t, owner.Valid, "empty owners must be explicit, not omitted")
			return string(owner.Value)
		}
	}
	t.Fatalf("selector %s has no owner", sel.Field)
	return ""
}
