package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/gitref"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspaceRemoteRef(t *testing.T) {
	t.Parallel()

	t.Run("supports address fragment ref at repository root", func(t *testing.T) {
		t.Parallel()

		for _, address := range []string{
			"https://github.com/dagger/dagger#main",
			"https://github.com/dagger/dagger#main:.",
		} {
			ref, err := ParseWorkspaceRemoteRef(context.Background(), address)
			require.NoError(t, err)
			require.Equal(t, "https://github.com/dagger/dagger", ref.CloneRef)
			require.Equal(t, "main", ref.Version)
			require.Equal(t, ".", ref.WorkspaceSubdir)
			require.Equal(t, gitref.GitRefSelector, ref.Selector)
		}
	})

	t.Run("supports address fragment ref and subdir", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main:toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.CloneRef)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, "toolchains/changelog", ref.WorkspaceSubdir)
		require.Equal(t, gitref.GitRefSelector, ref.Selector)
	})

	t.Run("supports legacy at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "github.com/dagger/dagger/toolchains/changelog@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, "toolchains/changelog", ref.WorkspaceSubdir)
		require.Equal(t, gitref.ModuleVersionSelector, ref.Selector)
	})

	t.Run("preserves legacy https at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, ".", ref.WorkspaceSubdir)
		require.Equal(t, gitref.ModuleVersionSelector, ref.Selector)
	})

	t.Run("resolves legacy vanity ref", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRefWithResolver(
			context.Background(),
			"dagger.io/go@main",
			func(_ context.Context, got string) (string, error) {
				require.Equal(t, "dagger.io/go@main", got)
				return "https://github.com/dagger/dagger/sdk/go@main", nil
			},
		)
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.CloneRef)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, "sdk/go", ref.WorkspaceSubdir)
	})

	t.Run("resolves fragment clone ref and preserves subdir", func(t *testing.T) {
		t.Parallel()

		ref, err := parseWorkspaceRemoteRefWithResolver(
			context.Background(),
			"https://github.com/dagger/python#main:docs",
			func(_ context.Context, got string) (string, error) {
				require.Equal(t, "https://github.com/dagger/python", got)
				return "https://github.com/dagger/dagger/sdk/go", nil
			},
		)
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.CloneRef)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, "sdk/go/docs", ref.WorkspaceSubdir)
		require.Equal(t, gitref.GitRefSelector, ref.Selector)
	})

	for _, invalid := range []string{
		"github.com/dagger/python/ruff#main",
		"https://github.com/dagger/python/ruff#main",
		"https://github.com/dagger/python#main:",
		"github.com/dagger/python@main:ruff",
		"https://github.com/dagger/python@main:ruff",
		"github.com/dagger/python#main:ruff",
	} {
		t.Run("rejects "+invalid, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWorkspaceRemoteRef(context.Background(), invalid)
			require.Error(t, err)
		})
	}
}

func TestWorkspaceGitRefSelectorSemverSemantics(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                   string
		selector               gitref.SelectorType
		supportsVersionQueries bool
		wantField              string
	}{
		{name: "at selector uses semver query", selector: gitref.ModuleVersionSelector, supportsVersionQueries: true, wantField: "latest"},
		{name: "old client uses literal git ref", selector: gitref.ModuleVersionSelector, supportsVersionQueries: false, wantField: "ref"},
		{name: "fragment selector uses literal git ref", selector: gitref.GitRefSelector, supportsVersionQueries: true, wantField: "ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			selector := workspaceGitRefSelector(WorkspaceRemoteRef{
				Version:         "v1.2",
				WorkspaceSubdir: "ruff",
				Selector:        tc.selector,
			}, tc.supportsVersionQueries)
			require.Equal(t, tc.wantField, selector.Field)
			if tc.wantField == "latest" {
				require.Equal(t, "version", selector.Args[0].Name)
				require.Equal(t, "tagPrefix", selector.Args[1].Name)
			}
		})
	}
}

func TestNormalizeWorkspaceRemoteSubdir(t *testing.T) {
	t.Parallel()

	t.Run("empty becomes dot", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeWorkspaceRemoteSubdir("")
		require.NoError(t, err)
		require.Equal(t, ".", got)
	})

	t.Run("absolute gets normalized to relative", func(t *testing.T) {
		t.Parallel()
		got, err := normalizeWorkspaceRemoteSubdir("/toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "toolchains/changelog", got)
	})

	t.Run("rejects escaping paths", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeWorkspaceRemoteSubdir("../outside")
		require.ErrorContains(t, err, "outside repository")
	})
}
