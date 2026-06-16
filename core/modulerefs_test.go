package core

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

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
	require.Error(t, err)

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
			case ModuleSourceKindLocal:
				require.NotNil(t, parsed.Local)
				require.Equal(t, tc.urlStr, parsed.Local.ModPath)
			}
		})
	}
}

type neverExistsFS struct {
}

func (fs neverExistsFS) Stat(ctx context.Context, path string) (string, *Stat, error) {
	return "", nil, os.ErrNotExist
}
