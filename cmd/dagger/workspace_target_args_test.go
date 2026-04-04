package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseChecksTargetArgs(t *testing.T) {
	t.Run("no separator keeps existing behavior", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"go:lint"}, -1)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"go:lint"}, patterns)
	})

	t.Run("separator with explicit workspace and pattern", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"github.com/acme/ws", "go:lint"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Equal(t, []string{"go:lint"}, patterns)
	})

	t.Run("separator with explicit workspace only", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"github.com/acme/ws"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Empty(t, patterns)
	})

	t.Run("separator without workspace", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"go:lint"}, 0)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"go:lint"}, patterns)
	})

	t.Run("separator with too many pre-target args errors", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"a", "b", "go:lint"}, 2)
		require.ErrorContains(t, err, "expected at most one workspace target before --")
		require.Nil(t, workspaceRef)
		require.Nil(t, patterns)
	})

	t.Run("no separator infers workspace for URL-like first arg", func(t *testing.T) {
		workspaceRef, patterns, err := parseChecksTargetArgs([]string{"https://github.com/dagger/dagger"}, -1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "https://github.com/dagger/dagger", *workspaceRef)
		require.Empty(t, patterns)
	})
}

func TestParseGenerateTargetArgs(t *testing.T) {
	t.Run("no separator keeps existing behavior", func(t *testing.T) {
		workspaceRef, patterns, err := parseGenerateTargetArgs([]string{"go:bin"}, -1)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"go:bin"}, patterns)
	})

	t.Run("separator with explicit workspace and pattern", func(t *testing.T) {
		workspaceRef, patterns, err := parseGenerateTargetArgs([]string{"github.com/acme/ws", "go:bin"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Equal(t, []string{"go:bin"}, patterns)
	})

	t.Run("separator with explicit workspace only", func(t *testing.T) {
		workspaceRef, patterns, err := parseGenerateTargetArgs([]string{"github.com/acme/ws"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Empty(t, patterns)
	})

	t.Run("separator without workspace", func(t *testing.T) {
		workspaceRef, patterns, err := parseGenerateTargetArgs([]string{"go:bin"}, 0)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"go:bin"}, patterns)
	})

	t.Run("separator with too many pre-target args errors", func(t *testing.T) {
		workspaceRef, patterns, err := parseGenerateTargetArgs([]string{"a", "b", "go:bin"}, 2)
		require.ErrorContains(t, err, "expected at most one workspace target before --")
		require.Nil(t, workspaceRef)
		require.Nil(t, patterns)
	})

	t.Run("no separator infers workspace for URL-like first arg", func(t *testing.T) {
		workspaceRef, patterns, err := parseGenerateTargetArgs([]string{"https://github.com/dagger/dagger"}, -1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "https://github.com/dagger/dagger", *workspaceRef)
		require.Empty(t, patterns)
	})
}

func TestParseFunctionsTargetArgs(t *testing.T) {
	t.Run("no separator keeps function args", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"container", "from"}, -1)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"container", "from"}, functions)
	})

	t.Run("separator with explicit workspace and function args", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"github.com/acme/ws", "container", "from"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Equal(t, []string{"container", "from"}, functions)
	})

	t.Run("separator with explicit workspace only", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"github.com/acme/ws"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "github.com/acme/ws", *workspaceRef)
		require.Empty(t, functions)
	})

	t.Run("separator without workspace", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"container", "from"}, 0)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"container", "from"}, functions)
	})

	t.Run("too many args before separator", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"a", "b", "container"}, 2)
		require.ErrorContains(t, err, "expected at most one workspace target before --")
		require.Nil(t, workspaceRef)
		require.Nil(t, functions)
	})

	t.Run("no separator infers workspace for URL-like first arg", func(t *testing.T) {
		workspaceRef, functions, err := parseFunctionsTargetArgs([]string{"https://github.com/dagger/dagger", "container"}, -1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "https://github.com/dagger/dagger", *workspaceRef)
		require.Equal(t, []string{"container"}, functions)
	})
}

func TestParseCallTargetArgs(t *testing.T) {
	t.Run("keeps function path when first arg is not workspace-like", func(t *testing.T) {
		workspaceRef, functionPath, err := parseCallTargetArgs([]string{"container", "from"}, -1)
		require.NoError(t, err)
		require.Nil(t, workspaceRef)
		require.Equal(t, []string{"container", "from"}, functionPath)
	})

	t.Run("infers workspace for URL-like first arg", func(t *testing.T) {
		workspaceRef, functionPath, err := parseCallTargetArgs([]string{"https://github.com/dagger/dagger", "container", "from"}, -1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "https://github.com/dagger/dagger", *workspaceRef)
		require.Equal(t, []string{"container", "from"}, functionPath)
	})

	t.Run("separator form with explicit workspace", func(t *testing.T) {
		workspaceRef, functionPath, err := parseCallTargetArgs([]string{"https://github.com/dagger/dagger", "container", "from"}, 1)
		require.NoError(t, err)
		require.NotNil(t, workspaceRef)
		require.Equal(t, "https://github.com/dagger/dagger", *workspaceRef)
		require.Equal(t, []string{"container", "from"}, functionPath)
	})
}

func TestStripHelpArgs(t *testing.T) {
	require.Equal(t, []string{"https://github.com/dagger/dagger"}, stripHelpArgs([]string{"https://github.com/dagger/dagger", "--help"}))
	require.Equal(t, []string{"container", "from"}, stripHelpArgs([]string{"container", "-h", "from"}))
}
