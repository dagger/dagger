package schema

import (
	"context"
	"os"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/vcs"
	"github.com/stretchr/testify/require"
	fsutiltypes "github.com/tonistiigi/fsutil/types"
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

func TestIsSemver(t *testing.T) {
	require.True(t, isSemver("v1.0.0"))
	require.True(t, isSemver("v2.0.1"))
	require.False(t, isSemver("1.0.0"))
	require.False(t, isSemver("v1.0"))
	require.False(t, isSemver("v1"))
	require.False(t, isSemver("foo"))
}

// Test ParseRefString using an interface to control Host side effect
func TestParseRefString(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		urlStr          string
		want            *parsedRefString
		wantErrContains string
	}{
		// github
		{
			urlStr: "github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr:          "github.com/shykes/daggerverse.git/../../",
			wantErrContains: "git module source subpath points out of root",
		},
		{
			urlStr: "https://github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeHTTPS,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "http://github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeHTTP,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeSSH,
					sourceUser:     "git",
				},
			},
		},
		{
			urlStr: "ssh://user@github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeSSH,
					sourceUser:     "user",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://user@github.com/shykes/daggerverse/ci@version",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeSSH,
					sourceUser:     "user",
					modVersion:     "version",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci@version",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					scheme:         core.SchemeSSH,
					sourceUser:     "",
					modVersion:     "version",
				},
			},
		},

		// GitLab
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
					repoRoot:       &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
					repoRootSubdir: "depth1/depth2",
					scheme:         core.NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
					repoRoot:       &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork.git", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
					repoRootSubdir: "depth1/depth2",
					scheme:         core.NoScheme,
					sourceUser:     "",
				},
			},
		},

		// Edge case of RepoRootForImportPath
		// private GitLab: go-get unauthenticated returns obfuscated repo root
		// https://gitlab.com/gitlab-org/gitlab-foss/-/blob/master/lib/gitlab/middleware/go.rb#L210-221
		{
			urlStr: "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private/depth1/depth2",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private/depth1/depth2",
					repoRoot:       &vcs.RepoRoot{Root: "gitlab.com/dagger-modules/private", Repo: "https://gitlab.com/dagger-modules/private"},
					repoRootSubdir: "test/more/dagger-test-modules-private/depth1/depth2",
					scheme:         core.SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		// private GitLab with ref including .git: here we declaratively know where the separation between repo and subdir is
		{
			urlStr: "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/depth1/depth2",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/depth1/depth2",
					repoRoot:       &vcs.RepoRoot{Root: "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git", Repo: "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private"},
					repoRootSubdir: "depth1/depth2",
					scheme:         core.SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		// bitbucket
		{
			urlStr: "bitbucket.org/test-travail/test/depth1",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "bitbucket.org/test-travail/test/depth1",
					repoRoot:       &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test", Repo: "https://bitbucket.org/test-travail/test"},
					repoRootSubdir: "depth1",
					scheme:         core.NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "bitbucket.org/test-travail/test.git/depth1",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "bitbucket.org/test-travail/test.git/depth1",
					repoRoot:       &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test.git", Repo: "https://bitbucket.org/test-travail/test"},
					repoRootSubdir: "depth1",
					scheme:         core.NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "git@github.com:shykes/daggerverse/ci",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					repoRootSubdir: "ci",
					sourceUser:     "git",
					scheme:         core.SchemeSCPLike,
				},
			},
		},
		{
			urlStr: "github.com:shykes/daggerverse.git/ci@version",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					scheme:         core.SchemeSCPLike,
					repoRootSubdir: "ci",
					hasVersion:     true,
					modVersion:     "version",
				},
			},
		},
		{
			urlStr: "github.com:shykes/daggerverse/ci@version",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					repoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					scheme:         core.SchemeSCPLike,
					repoRootSubdir: "ci",
					hasVersion:     true,
					modVersion:     "version",
				},
			},
		},
		// Azure ref parsing
		{
			urlStr: "https://daggere2e@dev.azure.com/daggere2e/public/_git/dagger-test-modules/cool-sdk",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "dev.azure.com/daggere2e/public/_git/dagger-test-modules/cool-sdk",
					repoRoot:       &vcs.RepoRoot{Root: "dev.azure.com/daggere2e/public/_git/dagger-test-modules", Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules"},
					scheme:         core.SchemeHTTPS,
					repoRootSubdir: "cool-sdk",
					sourceUser:     "daggere2e",
				},
			},
		},
		// ⚠️ Azure does not allow to have SSH refs ending with .git
		{
			urlStr: "git@ssh.dev.azure.com:v3/daggere2e/public/dagger-test-modules/cool-sdk",
			want: &parsedRefString{
				kind: core.ModuleSourceKindGit,
				git: &parsedGitRefString{
					modPath:        "ssh.dev.azure.com/v3/daggere2e/public/dagger-test-modules/cool-sdk",
					repoRoot:       &vcs.RepoRoot{Root: "ssh.dev.azure.com:v3/daggere2e/public/dagger-test-modules", Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules"},
					scheme:         core.SchemeSCPLike,
					repoRootSubdir: "cool-sdk",
					sourceUser:     "git",
				},
			},
		},
	} {
		tc := tc
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsed, err := parseRefString(
				ctx,
				neverExistsFS{},
				tc.urlStr,
				"",
			)
			if tc.wantErrContains != "" {
				require.ErrorContains(t, err, tc.wantErrContains)
				return
			} else {
				require.NoError(t, err)
			}

			require.NotNil(t, parsed)
			require.Equal(t, tc.want.git.modPath, parsed.git.modPath)
			require.Equal(t, tc.want.kind, parsed.kind)
			if tc.want.git.repoRoot != nil {
				require.Equal(t, tc.want.git.repoRoot.Repo, parsed.git.repoRoot.Repo)
				require.Equal(t, tc.want.git.repoRoot.Root, parsed.git.repoRoot.Root)
			}
			require.Equal(t, tc.want.git.repoRootSubdir, parsed.git.repoRootSubdir)
			require.Equal(t, tc.want.git.scheme, parsed.git.scheme)
			require.Equal(t, tc.want.git.sourceUser, parsed.git.sourceUser)
		})
	}
}

type neverExistsFS struct {
}

func (fs neverExistsFS) stat(ctx context.Context, path string) (*fsutiltypes.Stat, error) {
	return nil, os.ErrNotExist
}

func TestModuleLocaLSource(t *testing.T) {
	schema := &moduleSourceSchema{}
	ctx := context.Background()

	src := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/home/user/dagger-test-modules",
		},
	}

	for _, tc := range []struct {
		name           string
		source         *core.ModuleSource
		expectedResult string
		expectError    bool
		fn             func(ctx context.Context, source *core.ModuleSource, args struct{}) (string, error)
	}{
		{
			name:           "Local module source commit",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceCommit,
		},
		{
			name:           "Local module source html repo url",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceHTMLRepoURL,
		},
		{
			name:           "Local module source version",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.fn(ctx, tc.source, struct{}{})
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, result)
			}
		})
	}
}
