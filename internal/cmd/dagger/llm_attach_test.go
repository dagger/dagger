package daggercmd

import (
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestSpawnSeed: an attached worker's changes are measured from the workspace
// it was spawned with, which its handle's recipe carries as the conversation
// it was spawned from. Handles that don't say fall back.
func TestSpawnSeed(t *testing.T) {
	named := func(name string) *ast.Type { return &ast.Type{NamedType: name, NonNull: true} }
	encode := func(id *call.ID) string {
		t.Helper()
		enc, err := id.Encode()
		require.NoError(t, err)
		return enc
	}
	workspace := call.New().Append(named("Workspace"), "currentWorkspace")
	seed := call.New().
		Append(named("LLM"), "llm").
		Append(named("LLM"), "withWorkspace", call.WithArgs(call.NewArgument("workspace", call.NewLiteralID(workspace), false)))
	for _, field := range []string{"agent", "spawn"} {
		agent := seed.Append(named("Agent"), field, call.WithArgs(call.NewArgument("name", call.NewLiteralString("worker"), false)))
		got, ok := spawnSeed(encode(agent))
		require.True(t, ok, field)
		var decoded call.ID
		require.NoError(t, decoded.Decode(string(got)))
		require.Equal(t, seed.Digest(), decoded.Digest(), "%s: the seed is the conversation the agent was spawned from", field)
	}

	// A handle whose recipe doesn't lead to a conversation says nothing.
	for name, id := range map[string]string{
		"root lookup":   encode(call.New().Append(named("Agent"), "agent", call.WithArgs(call.NewArgument("handle", call.NewLiteralString("h"), false)))),
		"engine result": encode(call.NewEngineResultID(42, call.NewType(named("Agent")))),
		"garbage":       "not an id",
	} {
		_, ok := spawnSeed(id)
		require.False(t, ok, name)
	}
	require.Nil(t, spawnWorkspace(&dagger.Client{}, "not an id"))
}
