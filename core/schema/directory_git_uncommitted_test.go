package schema

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// Directory.__withGitUncommitted reads the calling client's checkout, so its
// cache key must name the client and its recipes must not be replayed
// without that client.
func TestWithGitUncommittedReadsTheCallingClient(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "client", SessionID: "session"})
	server := &currentTypeDefsTestServer{}
	base, err := NewCoreSchemaBase(ctx, server)
	require.NoError(t, err)
	srv, err := base.Fork(ctx, core.NewRoot(server), "v1.0.0")
	require.NoError(t, err)
	server.dag = srv

	t.Run("cached per client", func(t *testing.T) {
		dirType, ok := srv.ObjectType("Directory")
		require.True(t, ok)
		spec, ok := dirType.FieldSpec("__withGitUncommitted", "v1.0.0")
		require.True(t, ok)
		var inputs []string
		for _, input := range spec.ImplicitInputs {
			inputs = append(inputs, input.Name)
		}
		require.Contains(t, inputs, dagql.PerClientInput.Name)
	})

	t.Run("not replayable", func(t *testing.T) {
		dirType := (&core.Directory{}).Type()
		id := call.New().Append(dirType, "directory", call.WithView("v1.0.0")).
			Append(dirType, "__withGitUncommitted", call.WithView("v1.0.0"), call.WithArgs(
				call.NewArgument("checkoutPath", call.NewLiteralString("/work"), false),
				call.NewArgument("expectedHeadSHA", call.NewLiteralString(strings.Repeat("a", 40)), false),
			))
		classification := srv.ClassifyRecipe(id)
		require.NotNil(t, classification.NotReplayable)
		require.Equal(t, "__withGitUncommitted", classification.NotReplayable.Field)
	})
}
