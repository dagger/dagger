package gitref

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/vcs"
)

// TestParse covers git ref string parsing across the supported hosts and
// schemes. The higher-level kind detection / local-path fallback is covered by
// core.TestParseRefString.
func TestParse(t *testing.T) {
	for _, tc := range []struct {
		urlStr          string
		want            Parsed
		wantErrContains string
	}{
		// github
		{
			urlStr: "github.com/shykes/daggerverse/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         NoScheme,
				SourceUser:     "",
			},
		},
		{
			urlStr: "github.com/shykes/daggerverse.git/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse.git/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         NoScheme,
				SourceUser:     "",
			},
		},
		{
			urlStr:          "github.com/shykes/daggerverse.git/../../",
			wantErrContains: "git module source subpath points out of root",
		},
		{
			urlStr: "https://github.com/shykes/daggerverse/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         SchemeHTTPS,
				SourceUser:     "",
			},
		},
		{
			urlStr: "http://github.com/shykes/daggerverse.git/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse.git/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         SchemeHTTP,
				SourceUser:     "",
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse.git/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse.git/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         SchemeSSH,
				SourceUser:     "",
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         SchemeSSH,
				SourceUser:     "",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse.git/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         SchemeSSH,
				SourceUser:     "git",
			},
		},
		{
			urlStr: "ssh://user@github.com/shykes/daggerverse/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         SchemeSSH,
				SourceUser:     "user",
			},
		},
		{
			urlStr: "ssh://user@github.com/shykes/daggerverse/ci@version",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         SchemeSSH,
				SourceUser:     "user",
				ModVersion:     "version",
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci@version",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				Scheme:         SchemeSSH,
				SourceUser:     "",
				ModVersion:     "version",
			},
		},

		// GitLab
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
			want: Parsed{
				ModPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
				RepoRoot:       &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
				RepoRootSubdir: "depth1/depth2",
				Scheme:         NoScheme,
				SourceUser:     "",
			},
		},
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
			want: Parsed{
				ModPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
				RepoRoot:       &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork.git", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
				RepoRootSubdir: "depth1/depth2",
				Scheme:         NoScheme,
				SourceUser:     "",
			},
		},

		// Edge case of RepoRootForImportPath
		// private GitLab: go-get unauthenticated returns obfuscated repo root
		// https://gitlab.com/gitlab-org/gitlab-foss/-/blob/master/lib/gitlab/middleware/go.rb#L210-221
		{
			urlStr: "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private/depth1/depth2",
			want: Parsed{
				ModPath:        "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private/depth1/depth2",
				RepoRoot:       &vcs.RepoRoot{Root: "gitlab.com/dagger-modules/private", Repo: "https://gitlab.com/dagger-modules/private"},
				RepoRootSubdir: "test/more/dagger-test-modules-private/depth1/depth2",
				Scheme:         SchemeSSH,
				SourceUser:     "",
			},
		},
		// private GitLab with ref including .git: here we declaratively know where the separation between repo and subdir is
		{
			urlStr: "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/depth1/depth2",
			want: Parsed{
				ModPath:        "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/depth1/depth2",
				RepoRoot:       &vcs.RepoRoot{Root: "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git", Repo: "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private"},
				RepoRootSubdir: "depth1/depth2",
				Scheme:         SchemeSSH,
				SourceUser:     "",
			},
		},
		// bitbucket
		{
			urlStr: "bitbucket.org/test-travail/test/depth1",
			want: Parsed{
				ModPath:        "bitbucket.org/test-travail/test/depth1",
				RepoRoot:       &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test", Repo: "https://bitbucket.org/test-travail/test"},
				RepoRootSubdir: "depth1",
				Scheme:         NoScheme,
				SourceUser:     "",
			},
		},
		{
			urlStr: "bitbucket.org/test-travail/test.git/depth1",
			want: Parsed{
				ModPath:        "bitbucket.org/test-travail/test.git/depth1",
				RepoRoot:       &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test.git", Repo: "https://bitbucket.org/test-travail/test"},
				RepoRootSubdir: "depth1",
				Scheme:         NoScheme,
				SourceUser:     "",
			},
		},
		{
			urlStr: "git@github.com:shykes/daggerverse/ci",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
				SourceUser:     "git",
				Scheme:         SchemeSCPLike,
			},
		},
		{
			urlStr: "github.com:shykes/daggerverse.git/ci@version",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse.git/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				Scheme:         SchemeSCPLike,
				RepoRootSubdir: "ci",
				HasVersion:     true,
				ModVersion:     "version",
			},
		},
		{
			urlStr: "github.com:shykes/daggerverse/ci@version",
			want: Parsed{
				ModPath:        "github.com/shykes/daggerverse/ci",
				RepoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				Scheme:         SchemeSCPLike,
				RepoRootSubdir: "ci",
				HasVersion:     true,
				ModVersion:     "version",
			},
		},
		// Azure ref parsing
		{
			urlStr: "https://daggere2e@dev.azure.com/daggere2e/public/_git/dagger-test-modules/cool-sdk",
			want: Parsed{
				ModPath:        "dev.azure.com/daggere2e/public/_git/dagger-test-modules/cool-sdk",
				RepoRoot:       &vcs.RepoRoot{Root: "dev.azure.com/daggere2e/public/_git/dagger-test-modules", Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules"},
				Scheme:         SchemeHTTPS,
				RepoRootSubdir: "cool-sdk",
				SourceUser:     "daggere2e",
			},
		},
		// ⚠️ Azure does not allow to have SSH refs ending with .git
		{
			urlStr: "git@ssh.dev.azure.com:v3/daggere2e/public/dagger-test-modules/cool-sdk",
			want: Parsed{
				ModPath:        "ssh.dev.azure.com/v3/daggere2e/public/dagger-test-modules/cool-sdk",
				RepoRoot:       &vcs.RepoRoot{Root: "ssh.dev.azure.com:v3/daggere2e/public/dagger-test-modules", Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules"},
				Scheme:         SchemeSCPLike,
				RepoRootSubdir: "cool-sdk",
				SourceUser:     "git",
			},
		},
		// Gerrit codereview on custom port
		{
			urlStr: "ssh://someuser@golang.org:29418/x/review/git-codereview",
			want: Parsed{
				ModPath:        "golang.org/x/review/git-codereview",
				RepoRoot:       &vcs.RepoRoot{Root: "golang.org/x/review", Repo: "https://go.googlesource.com/review"},
				Scheme:         SchemeSSH,
				RepoRootSubdir: "git-codereview",
				SourceUser:     "someuser",
				CloneRef:       "ssh://someuser@golang.org:29418/x/review",
				SourceCloneRef: "ssh://someuser@golang.org:29418/x/review",
			},
		},
	} {
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsed, err := Parse(context.Background(), tc.urlStr)
			if tc.wantErrContains != "" {
				require.ErrorContains(t, err, tc.wantErrContains)
				return
			}
			require.NoError(t, err)

			require.Equal(t, tc.want.ModPath, parsed.ModPath)
			if tc.want.RepoRoot != nil {
				require.Equal(t, tc.want.RepoRoot.Repo, parsed.RepoRoot.Repo)
				require.Equal(t, tc.want.RepoRoot.Root, parsed.RepoRoot.Root)
			}
			require.Equal(t, tc.want.RepoRootSubdir, parsed.RepoRootSubdir)
			require.Equal(t, tc.want.Scheme, parsed.Scheme)
			require.Equal(t, tc.want.SourceUser, parsed.SourceUser)

			if tc.want.SourceCloneRef != "" {
				require.Equal(t, tc.want.SourceCloneRef, parsed.SourceCloneRef)
			}
			if tc.want.CloneRef != "" {
				require.Equal(t, tc.want.CloneRef, parsed.CloneRef)
			}
		})
	}
}
