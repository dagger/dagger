package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalGlobalSettingsKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		key  string
		want string
	}{
		{
			name: "git ref with subpath",
			key:  "github.com/dagger/dagger/modules/go",
			want: "github.com/dagger/dagger/modules/go",
		},
		{
			name: "git ref without subpath",
			key:  "github.com/shykes/hello",
			want: "github.com/shykes/hello",
		},
		{
			name: "git ref with version",
			key:  "github.com/dagger/dagger/modules/go@v0.19",
			want: "github.com/dagger/dagger/modules/go@v0.19",
		},
		{
			name: "https scheme normalizes to plain ref",
			key:  "https://github.com/dagger/dagger/modules/go",
			want: "github.com/dagger/dagger/modules/go",
		},
		{
			name: "dot git suffix normalizes to plain ref",
			key:  "github.com/dagger/dagger.git/modules/go",
			want: "github.com/dagger/dagger/modules/go",
		},
		{
			name: "scp-like ssh ref normalizes to plain ref",
			key:  "git@github.com:dagger/dagger/modules/go",
			want: "github.com/dagger/dagger/modules/go",
		},
		{
			name: "absolute local path",
			key:  "/work/vendored/go-toolchain",
			want: "/work/vendored/go-toolchain",
		},
		{
			name: "absolute local path is cleaned",
			key:  "/work/vendored/../vendored/go-toolchain/",
			want: "/work/vendored/go-toolchain",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CanonicalGlobalSettingsKey(ctx, tc.key)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestGlobalSettingsForSource(t *testing.T) {
	t.Parallel()

	gitSrc := func(cloneRef, subpath, version string) *ModuleSource {
		return &ModuleSource{
			Kind:              ModuleSourceKindGit,
			SourceRootSubpath: subpath,
			Git: &GitModuleSource{
				CloneRef: cloneRef,
				Version:  version,
			},
		}
	}
	localSrc := func(contextPath, subpath string) *ModuleSource {
		return &ModuleSource{
			Kind:              ModuleSourceKindLocal,
			SourceRootSubpath: subpath,
			Local: &LocalModuleSource{
				ContextDirectoryPath: contextPath,
			},
		}
	}

	t.Run("matches a git source on any version", func(t *testing.T) {
		t.Parallel()
		settings := map[string]map[string]any{
			"github.com/dagger/dagger/modules/go": {"version": "1.25"},
		}
		require.Equal(t, map[string]any{"version": "1.25"},
			GlobalSettingsForSource(gitSrc("github.com/dagger/dagger", "modules/go", "v0.19.0"), settings))
		require.Equal(t, map[string]any{"version": "1.25"},
			GlobalSettingsForSource(gitSrc("github.com/dagger/dagger", "modules/go", "main"), settings))
	})

	t.Run("matches scheme variants of the same git ref", func(t *testing.T) {
		t.Parallel()
		settings := map[string]map[string]any{
			"github.com/dagger/dagger/modules/go": {"version": "1.25"},
		}
		require.Equal(t, map[string]any{"version": "1.25"},
			GlobalSettingsForSource(gitSrc("https://github.com/dagger/dagger", "modules/go", ""), settings))
	})

	t.Run("a version in the key restricts the match", func(t *testing.T) {
		t.Parallel()
		settings := map[string]map[string]any{
			"github.com/dagger/dagger/modules/go@v0.19.0": {"version": "1.25"},
		}
		require.Equal(t, map[string]any{"version": "1.25"},
			GlobalSettingsForSource(gitSrc("github.com/dagger/dagger", "modules/go", "v0.19.0"), settings))
		require.Nil(t,
			GlobalSettingsForSource(gitSrc("github.com/dagger/dagger", "modules/go", "v0.20.0"), settings))
	})

	t.Run("a versioned entry wins over a version-agnostic one", func(t *testing.T) {
		t.Parallel()
		settings := map[string]map[string]any{
			"github.com/dagger/dagger/modules/go":         {"version": "1.24"},
			"github.com/dagger/dagger/modules/go@v0.19.0": {"version": "1.25"},
		}
		require.Equal(t, map[string]any{"version": "1.25"},
			GlobalSettingsForSource(gitSrc("github.com/dagger/dagger", "modules/go", "v0.19.0"), settings))
		require.Equal(t, map[string]any{"version": "1.24"},
			GlobalSettingsForSource(gitSrc("github.com/dagger/dagger", "modules/go", "v0.20.0"), settings))
	})

	t.Run("does not match a different subpath", func(t *testing.T) {
		t.Parallel()
		settings := map[string]map[string]any{
			"github.com/dagger/dagger/modules/go": {"version": "1.25"},
		}
		require.Nil(t,
			GlobalSettingsForSource(gitSrc("github.com/dagger/dagger", "modules/wolfi", ""), settings))
	})

	t.Run("matches a local source by context directory and subpath", func(t *testing.T) {
		t.Parallel()
		settings := map[string]map[string]any{
			"/work/vendored/go-toolchain": {"version": "1.25"},
		}
		require.Equal(t, map[string]any{"version": "1.25"},
			GlobalSettingsForSource(localSrc("/work", "vendored/go-toolchain"), settings))
		require.Nil(t,
			GlobalSettingsForSource(localSrc("/work", "vendored/other"), settings))
	})

	t.Run("returns nil when there are no settings", func(t *testing.T) {
		t.Parallel()
		require.Nil(t,
			GlobalSettingsForSource(gitSrc("github.com/dagger/dagger", "modules/go", ""), nil))
	})
}
