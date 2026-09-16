package entrypointmodule

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPushChainDetectsRepeatedDirectory(t *testing.T) {
	t.Parallel()

	ctx, err := pushChain(t.Context(), "app", "dir-a")
	require.NoError(t, err)
	ctx, err = pushChain(ctx, "middle", "dir-b")
	require.NoError(t, err)

	// Returning to a directory already in the chain is a cycle, and the error
	// names every module on the way back to it.
	_, err = pushChain(ctx, "back-to-app", "dir-a")
	require.ErrorContains(t, err, "module entrypoint cycle: app -> middle -> back-to-app")
}

func TestPushChainAllowsDistinctDirectories(t *testing.T) {
	t.Parallel()

	ctx, err := pushChain(t.Context(), "app", "dir-a")
	require.NoError(t, err)
	ctx, err = pushChain(ctx, "middle", "dir-b")
	require.NoError(t, err)
	_, err = pushChain(ctx, "leaf", "dir-c")
	require.NoError(t, err)
}
