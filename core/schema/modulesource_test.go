package schema

import (
	"context"
	"os"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/vcs"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil/types"
)

// Test ParseRefString using an interface to control Host side effect
func TestParseRefString(t *testing.T) {
	ctx := context.Background()

	bkClientDirFalse := &MockBuildkitClient{
		StatFunc: func(ctx context.Context, path string, followLinks bool) (*types.Stat, error) {
			return &types.Stat{Mode: uint32(os.ModeDevice)}, nil // stat.Dir() returns false
		},
	}

	for _, tc := range []struct {
		urlStr     string
		mockClient buildkitClient
		want       *parsedRefString
	}{
		// github
		{
			urlStr: "github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.NoScheme,
				sshusername:    "",
			},
		},
		{
			urlStr: "github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse.git/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.NoScheme,
				sshusername:    "",
			},
		},
		{
			urlStr: "github.com/shykes/daggerverse.git/../../",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse.git/../../",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "../../",
				scheme:         core.NoScheme,
				sshusername:    "",
			},
		},
		{
			urlStr: "https://github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeHTTPS,
				sshusername:    "",
			},
		},
		{
			urlStr: "http://github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse.git/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeHTTP,
				sshusername:    "",
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse.git/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeSSH,
				sshusername:    "",
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeSSH,
				sshusername:    "",
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse.git/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeSSH,
				sshusername:    "",
			},
		},
		{
			urlStr: "ssh://git@github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse.git/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeSSH,
				sshusername:    "git",
			},
		},
		{
			urlStr: "ssh://user@github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeSSH,
				sshusername:    "user",
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeSSH,
				sshusername:    "",
			},
		},
		{
			urlStr: "ssh://user@github.com/shykes/daggerverse/ci@version",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeSSH,
				sshusername:    "user",
				modVersion:     "version",
			},
		},
		{
			urlStr: "ssh://github.com/shykes/daggerverse/ci@version",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				scheme:         core.SchemeSSH,
				sshusername:    "",
				modVersion:     "version",
			},
		},

		// GitLab
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
			want: &parsedRefString{
				modPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
				repoRootSubdir: "depth1/depth2",
				scheme:         core.NoScheme,
				sshusername:    "",
			},
		},
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
			want: &parsedRefString{
				modPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork.git", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
				repoRootSubdir: "depth1/depth2",
				scheme:         core.NoScheme,
				sshusername:    "",
			},
		},

		// Edge case of RepoRootForImportPath
		// private GitLab: go-get unauthenticated returns obfuscated repo root
		// https://gitlab.com/gitlab-org/gitlab-foss/-/blob/master/lib/gitlab/middleware/go.rb#L210-221
		{
			urlStr: "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private/depth1/depth2",
			want: &parsedRefString{
				modPath:        "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private/depth1/depth2",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "gitlab.com/dagger-modules/private", Repo: "https://gitlab.com/dagger-modules/private"},
				repoRootSubdir: "test/more/dagger-test-modules-private/depth1/depth2",
				scheme:         core.SchemeSSH,
				sshusername:    "",
			},
		},
		// private GitLab with ref including .git: here we declaratively know where the separation between repo and subdir is
		{
			urlStr: "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/depth1/depth2",
			want: &parsedRefString{
				modPath:        "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git/depth1/depth2",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git", Repo: "https://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private"},
				repoRootSubdir: "depth1/depth2",
				scheme:         core.SchemeSSH,
				sshusername:    "",
			},
		},
		// bitbucket
		{
			urlStr: "bitbucket.org/test-travail/test/depth1",
			want: &parsedRefString{
				modPath:        "bitbucket.org/test-travail/test/depth1",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test", Repo: "https://bitbucket.org/test-travail/test"},
				repoRootSubdir: "depth1",
				scheme:         core.NoScheme,
				sshusername:    "",
			},
		},
		{
			urlStr: "bitbucket.org/test-travail/test.git/depth1",
			want: &parsedRefString{
				modPath:        "bitbucket.org/test-travail/test.git/depth1",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test.git", Repo: "https://bitbucket.org/test-travail/test"},
				repoRootSubdir: "depth1",
				scheme:         core.NoScheme,
				sshusername:    "",
			},
		},
		{
			urlStr: "git@github.com:shykes/daggerverse/ci",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				repoRootSubdir: "ci",
				sshusername:    "git",
				scheme:         core.SchemeSCPLike,
			},
		},
		{
			urlStr: "github.com:shykes/daggerverse.git/ci@version",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse.git/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				scheme:         core.SchemeSCPLike,
				repoRootSubdir: "ci",
				hasVersion:     true,
				modVersion:     "version",
			},
		},
		{
			urlStr: "github.com:shykes/daggerverse/ci@version",
			want: &parsedRefString{
				modPath:        "github.com/shykes/daggerverse/ci",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "github.com:shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				scheme:         core.SchemeSCPLike,
				repoRootSubdir: "ci",
				hasVersion:     true,
				modVersion:     "version",
			},
		},
		// Azure ref parsing
		{
			urlStr: "https://daggere2e@dev.azure.com/daggere2e/public/_git/dagger-test-modules/cool-sdk",
			want: &parsedRefString{
				modPath:        "dev.azure.com/daggere2e/public/_git/dagger-test-modules/cool-sdk",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "dev.azure.com/daggere2e/public/_git/dagger-test-modules", Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules"},
				scheme:         core.SchemeHTTPS,
				repoRootSubdir: "cool-sdk",
				sshusername:    "daggere2e",
			},
		},
		// ⚠️ Azure does not allow to have SSH refs ending with .git
		{
			urlStr: "git@ssh.dev.azure.com:v3/daggere2e/public/dagger-test-modules/cool-sdk",
			want: &parsedRefString{
				modPath:        "ssh.dev.azure.com/v3/daggere2e/public/dagger-test-modules/cool-sdk",
				kind:           core.ModuleSourceKindGit,
				repoRoot:       &vcs.RepoRoot{Root: "ssh.dev.azure.com:v3/daggere2e/public/dagger-test-modules", Repo: "https://dev.azure.com/daggere2e/public/_git/dagger-test-modules"},
				scheme:         core.SchemeSCPLike,
				repoRootSubdir: "cool-sdk",
				sshusername:    "git",
			},
		},
	} {
		tc := tc
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsed := parseRefString(ctx, bkClientDirFalse, tc.urlStr)
			require.NotNil(t, parsed)
			require.Equal(t, tc.want.modPath, parsed.modPath)
			require.Equal(t, tc.want.kind, parsed.kind)
			if tc.want.repoRoot != nil {
				require.Equal(t, tc.want.repoRoot.Repo, parsed.repoRoot.Repo)
				require.Equal(t, tc.want.repoRoot.Root, parsed.repoRoot.Root)
			}
			require.Equal(t, tc.want.repoRootSubdir, parsed.repoRootSubdir)
			require.Equal(t, tc.want.scheme, parsed.scheme)
			require.Equal(t, tc.want.sshusername, parsed.sshusername)

			require.Equal(t, tc.urlStr, parsed.String())
		})
	}
}

// Mock BuildKit StatCallerHostPath call
type MockBuildkitClient struct {
	StatFunc func(ctx context.Context, path string, followLinks bool) (*types.Stat, error)
}

func (m *MockBuildkitClient) StatCallerHostPath(ctx context.Context, path string, followLinks bool) (*types.Stat, error) {
	return m.StatFunc(ctx, path, followLinks)
}
