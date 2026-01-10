package core

import (
	"context"
	"os"
	"testing"

	"github.com/dagger/dagger/engine/vcs"
	fsutiltypes "github.com/dagger/dagger/internal/fsutil/types"
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

// Test ParseRefString using an interface to control Host side effect
func TestParseRefString(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		urlStr          string
		want            *ParsedRefString
		wantErrContains string
	}{
		// github
		{
			urlStr: "github.com/shykes/daggerverse/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "github.com/shykes/daggerverse.git/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         NoScheme,
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
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeHTTPS,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "http://github.com/shykes/daggerverse.git/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeHTTP,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse.git/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse.git/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeSSH,
					sourceUser:     "git",
				},
			},
		},
		{
			urlStr: "ssh://user@github.com/shykes/daggerverse/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeSSH,
					sourceUser:     "user",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "ssh://user@github.com/shykes/daggerverse/ci@version",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeSSH,
					sourceUser:     "user",
					ModVersion:     "version",
				},
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci@version",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					scheme:         SchemeSSH,
					sourceUser:     "",
					ModVersion:     "version",
				},
			},
		},

		// GitLab
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
					RepoRoot:       &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
					RepoRootSubdir: "depth1/depth2",
					scheme:         NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
					RepoRoot:       &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork.git", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
					RepoRootSubdir: "depth1/depth2",
					scheme:         NoScheme,
					sourceUser:     "",
				},
			},
		},

		// Edge case of RepoRootForImportPath
		// private GitLab: go-get unauthenticated returns obfuscated repo root
		// https://gitlab.com/gitlab-org/gitlab-foss/-/blob/master/lib/gitlab/middleware/go.rb#L210-221
		{
			urlStr: "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private/depth1/depth2",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private/depth1/depth2",
					RepoRoot:       &vcs.RepoRoot{Root: "gitlab.com/dagger-modules/private", Repo: "https://gitlab.com/dagger-modules/private"},
					RepoRootSubdir: "test/more/dagger-test-modules-private/depth1/depth2",
					scheme:         SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		// private GitLab with ref including .git: here we declaratively know where the separation between repo and subdir is
		{
			urlStr: "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/depth1/depth2",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/depth1/depth2",
					RepoRoot:       &vcs.RepoRoot{Root: "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git", Repo: "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private"},
					RepoRootSubdir: "depth1/depth2",
					scheme:         SchemeSSH,
					sourceUser:     "",
				},
			},
		},
		// bitbucket
		{
			urlStr: "bitbucket.org/test-travail/test/depth1",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "bitbucket.org/test-travail/test/depth1",
					RepoRoot:       &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test", Repo: "https://bitbucket.org/test-travail/test"},
					RepoRootSubdir: "depth1",
					scheme:         NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "bitbucket.org/test-travail/test.git/depth1",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "bitbucket.org/test-travail/test.git/depth1",
					RepoRoot:       &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test.git", Repo: "https://bitbucket.org/test-travail/test"},
					RepoRootSubdir: "depth1",
					scheme:         NoScheme,
					sourceUser:     "",
				},
			},
		},
		{
			urlStr: "git@github.com:shykes/daggerverse/ci",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					RepoRootSubdir: "ci",
					sourceUser:     "git",
					scheme:         SchemeSCPLike,
				},
			},
		},
		{
			urlStr: "github.com:shykes/daggerverse.git/ci@version",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse.git/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
					scheme:         SchemeSCPLike,
					RepoRootSubdir: "ci",
					hasVersion:     true,
					ModVersion:     "version",
				},
			},
		},
		{
			urlStr: "github.com:shykes/daggerverse/ci@version",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "github.com/shykes/daggerverse/ci",
					RepoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
					scheme:         SchemeSCPLike,
					RepoRootSubdir: "ci",
					hasVersion:     true,
					ModVersion:     "version",
				},
			},
		},
		// Azure ref parsing
		{
			urlStr: "https://daggere2e@dev.azure.com/daggere2e/public/_git/dagger-test-modules/cool-sdk",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "dev.azure.com/daggere2e/public/_git/dagger-test-modules/cool-sdk",
					RepoRoot:       &vcs.RepoRoot{Root: "dev.azure.com/daggere2e/public/_git/dagger-test-modules", Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules"},
					scheme:         SchemeHTTPS,
					RepoRootSubdir: "cool-sdk",
					sourceUser:     "daggere2e",
				},
			},
		},
		// ⚠️ Azure does not allow to have SSH refs ending with .git
		{
			urlStr: "git@ssh.dev.azure.com:v3/daggere2e/public/dagger-test-modules/cool-sdk",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "ssh.dev.azure.com/v3/daggere2e/public/dagger-test-modules/cool-sdk",
					RepoRoot:       &vcs.RepoRoot{Root: "ssh.dev.azure.com:v3/daggere2e/public/dagger-test-modules", Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules"},
					scheme:         SchemeSCPLike,
					RepoRootSubdir: "cool-sdk",
					sourceUser:     "git",
				},
			},
		},
		// Gerrit codereview on custom port
		{
			urlStr: "ssh://someuser@golang.org:29418/x/review/git-codereview",
			want: &ParsedRefString{
				Kind: ModuleSourceKindGit,
				Git: &ParsedGitRefString{
					modPath:        "golang.org/x/review/git-codereview",
					RepoRoot:       &vcs.RepoRoot{Root: "golang.org/x/review", Repo: "https://go.googlesource.com/review"},
					scheme:         SchemeSSH,
					RepoRootSubdir: "git-codereview",
					sourceUser:     "someuser",
					cloneRef:       "ssh://someuser@golang.org:29418/x/review",
					SourceCloneRef: "ssh://someuser@golang.org:29418/x/review",
				},
			},
		},
	} {
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsed, err := ParseRefString(
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
			require.Equal(t, tc.want.Git.modPath, parsed.Git.modPath)
			require.Equal(t, tc.want.Kind, parsed.Kind)
			if tc.want.Git.RepoRoot != nil {
				require.Equal(t, tc.want.Git.RepoRoot.Repo, parsed.Git.RepoRoot.Repo)
				require.Equal(t, tc.want.Git.RepoRoot.Root, parsed.Git.RepoRoot.Root)
			}
			require.Equal(t, tc.want.Git.RepoRootSubdir, parsed.Git.RepoRootSubdir)
			require.Equal(t, tc.want.Git.scheme, parsed.Git.scheme)
			require.Equal(t, tc.want.Git.sourceUser, parsed.Git.sourceUser)

			if tc.want.Git.SourceCloneRef != "" {
				require.Equal(t, tc.want.Git.SourceCloneRef, parsed.Git.SourceCloneRef)
			}

			if tc.want.Git.cloneRef != "" {
				require.Equal(t, tc.want.Git.cloneRef, parsed.Git.cloneRef)
			}
		})
	}
}

type neverExistsFS struct {
}

func (fs neverExistsFS) Stat(ctx context.Context, path string) (*fsutiltypes.Stat, error) {
	return nil, os.ErrNotExist
}
