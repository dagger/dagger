package core

import (
	"context"
	"os"
	"testing"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func TestModuleGitDefaultRefSelector(t *testing.T) {
	t.Parallel()

	parsed := &ParsedGitRefString{Parsed: gitref.Parsed{
		RepoRootSubdir: "/module",
	}}
	for _, tc := range []struct {
		name    string
		version string
		field   string
	}{
		{name: "legacy API uses HEAD", version: "v1.0.0-beta.9", field: "head"},
		{name: "current API uses latest", version: workspace.LatestReleaseVersion, field: "latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := dagql.ContextWithCall(t.Context(), &dagql.ResultCall{
				View: call.View(tc.version),
			})
			selector := moduleGitDefaultRefSelector(ctx, parsed)
			require.Equal(t, tc.field, selector.Field)
			if tc.field == "latest" {
				require.Len(t, selector.Args, 1)
				require.Equal(t, "tagPrefix", selector.Args[0].Name)
			} else {
				require.Empty(t, selector.Args)
			}
		})
	}
}

func TestParsedGitRefStringSetVersion(t *testing.T) {
	t.Parallel()

	parsed := &ParsedGitRefString{}
	require.NoError(t, parsed.SetVersion("v1.2-beta"))
	require.True(t, parsed.HasVersion)
	require.Equal(t, "v1.2-beta", parsed.ModVersion)
	require.Equal(t, gitref.ModuleVersionSelector, parsed.Selector)

	require.EqualError(t,
		parsed.SetVersion("v2"),
		`version query "v2" cannot be used because the module source ref already has version "v1.2-beta"`,
	)

	invalid := &ParsedGitRefString{}
	require.ErrorContains(t, invalid.SetVersion("main"), `invalid version query "main"`)
}

func TestParsedGitRefStringVersionQuerySemantics(t *testing.T) {
	t.Parallel()

	ctx := dagql.ContextWithCall(t.Context(), &dagql.ResultCall{
		View: call.View(workspace.VersionQueriesVersion),
	})
	for _, tc := range []struct {
		name     string
		selector gitref.SelectorType
		want     string
	}{
		{name: "at selector uses semver query", selector: gitref.ModuleVersionSelector, want: "v1.2"},
		{name: "fragment selector stays literal", selector: gitref.GitRefSelector, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed := &ParsedGitRefString{Parsed: gitref.Parsed{
				ModVersion: "v1.2",
				HasVersion: true,
				Selector:   tc.selector,
			}}
			require.Equal(t, tc.want, parsed.versionQuery(ctx))
		})
	}
}

func TestMatchVersion(t *testing.T) {
	vers := []string{"v1.0.0", "v1.0.1", "v2.0.0", "path/v1.0.1", "path/v2.0.1"}

	match1, err := matchVersion(vers, "v1.0.1", "/")
	require.NoError(t, err)
	require.Equal(t, "v1.0.1", match1)

	match2, err := matchVersion(vers, "v1.0.1", "path")
	require.NoError(t, err)
	require.Equal(t, "path/v1.0.1", match2)

	match3, err := matchVersion(vers, "v1.0.1", "/path")
	require.NoError(t, err)
	require.Equal(t, "path/v1.0.1", match3)

	_, err = matchVersion(vers, "v2.0.1", "/")
	require.ErrorIs(t, err, ErrModuleVersionNotFound)

	_, err = matchVersion([]string{"hello/v0.3.0"}, "v0.3.0", "/hello")
	require.NoError(t, err)
}

// TestParseRefString covers the kind detection and git/local delegation done by
// ParseRefString. The exhaustive git ref parsing matrix lives in
// core/gitref.TestParse.
func TestParseRefString(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		urlStr          string
		wantKind        ModuleSourceKind
		wantCloneRef    string
		wantSubdir      string
		wantVersion     string
		wantSelector    gitref.SelectorType
		wantErrContains string
	}{
		{
			urlStr:       "github.com/shykes/daggerverse/ci",
			wantKind:     ModuleSourceKindGit,
			wantCloneRef: "github.com/shykes/daggerverse",
			wantSubdir:   "ci",
		},
		{
			urlStr:       "ssh://github.com/shykes/daggerverse/ci@version",
			wantKind:     ModuleSourceKindGit,
			wantCloneRef: "ssh://github.com/shykes/daggerverse",
			wantSubdir:   "ci",
			wantVersion:  "version",
			wantSelector: gitref.ModuleVersionSelector,
		},
		{
			urlStr:       "https://github.com/shykes/daggerverse#version:ci",
			wantKind:     ModuleSourceKindGit,
			wantCloneRef: "https://github.com/shykes/daggerverse",
			wantSubdir:   "ci",
			wantVersion:  "version",
			wantSelector: gitref.GitRefSelector,
		},
		{
			urlStr:       "github.com/dagger/python/ruff@main",
			wantKind:     ModuleSourceKindGit,
			wantCloneRef: "github.com/dagger/python",
			wantSubdir:   "ruff",
			wantVersion:  "main",
			wantSelector: gitref.ModuleVersionSelector,
		},
		{
			urlStr:       "https://github.com/dagger/python/ruff@main",
			wantKind:     ModuleSourceKindGit,
			wantCloneRef: "https://github.com/dagger/python",
			wantSubdir:   "ruff",
			wantVersion:  "main",
			wantSelector: gitref.ModuleVersionSelector,
		},
		{
			urlStr:       "https://github.com/dagger/python#main:ruff",
			wantKind:     ModuleSourceKindGit,
			wantCloneRef: "https://github.com/dagger/python",
			wantSubdir:   "ruff",
			wantVersion:  "main",
			wantSelector: gitref.GitRefSelector,
		},
		{
			urlStr:       "https://github.com/dagger/python#main",
			wantKind:     ModuleSourceKindGit,
			wantCloneRef: "https://github.com/dagger/python",
			wantSubdir:   "/",
			wantVersion:  "main",
			wantSelector: gitref.GitRefSelector,
		},
		{
			urlStr:          "github.com/dagger/python/ruff#main",
			wantErrContains: "requires an explicit protocol",
		},
		{
			urlStr:          "https://github.com/dagger/python/ruff#main",
			wantErrContains: "repository root is \"github.com/dagger/python\"",
		},
		{
			urlStr:          "github.com/dagger/python@main:ruff",
			wantErrContains: "invalid module version selector",
		},
		{
			urlStr:          "https://github.com/dagger/python@main:ruff",
			wantErrContains: "invalid module version selector",
		},
		{
			urlStr:          "github.com/dagger/python#main:ruff",
			wantErrContains: "requires an explicit protocol",
		},
		{
			// no dot in the ref string: treated as a local path
			urlStr:     "./some/local/path",
			wantKind:   ModuleSourceKindLocal,
			wantSubdir: "",
		},
		{
			urlStr:          "github.com/shykes/daggerverse.git/../../",
			wantErrContains: "git module source subpath points out of root",
		},
	} {
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsed, err := ParseRefString(ctx, neverExistsFS{}, tc.urlStr, "")
			if tc.wantErrContains != "" {
				require.ErrorContains(t, err, tc.wantErrContains)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, parsed)
			require.Equal(t, tc.wantKind, parsed.Kind)

			switch tc.wantKind {
			case ModuleSourceKindGit:
				require.NotNil(t, parsed.Git)
				require.Equal(t, tc.wantCloneRef, parsed.Git.SourceCloneRef)
				require.Equal(t, tc.wantSubdir, parsed.Git.RepoRootSubdir)
				require.Equal(t, tc.wantVersion, parsed.Git.ModVersion)
				require.Equal(t, tc.wantSelector, parsed.Git.Selector)
			case ModuleSourceKindLocal:
				require.NotNil(t, parsed.Local)
				require.Equal(t, tc.urlStr, parsed.Local.ModPath)
			}
		})
	}
}

func TestParseRefStringRemoteFailure(t *testing.T) {
	// A malformed known-host reference fails without needing network access.
	remote := "github.com/missing@v1"
	parsed, err := ParseRefString(t.Context(), neverExistsFS{}, remote, "")
	require.Nil(t, parsed)
	require.ErrorContains(t, err, `resolve remote module "github.com/missing@v1"`)
	require.ErrorContains(t, err, "invalid github.com/ import path")
	require.NotContains(t, err.Error(), "local path")

	exists := StatFSFunc(func(context.Context, string) (string, *Stat, error) {
		return remote, &Stat{FileType: FileTypeDirectory}, nil
	})
	parsed, err = ParseRefString(t.Context(), exists, remote, "")
	require.NoError(t, err)
	require.Equal(t, ModuleSourceKindLocal, parsed.Kind)

	parsed, err = ParseRefString(t.Context(), neverExistsFS{}, "./"+remote, "")
	require.NoError(t, err)
	require.Equal(t, ModuleSourceKindLocal, parsed.Kind)
}

type neverExistsFS struct {
}

func (fs neverExistsFS) Stat(ctx context.Context, path string) (string, *Stat, error) {
	return "", nil, os.ErrNotExist
}
