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
// It only tests public repos
func TestParsePublicRefString(t *testing.T) {
	ctx := context.Background()

	bkClientDirFalse := &MockBuildkitClient{
		StatFunc: func(ctx context.Context, path string, followLinks bool) (*types.Stat, error) {
			return &types.Stat{Mode: uint32(os.ModeDevice)}, nil // stat.Dir() returns false
		},
	}

	for _, tc := range []struct {
		urlStr     string
		mockClient BuildkitClient
		want       *parsedRefString
	}{
		// github
		{
			urlStr: "github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				modPath:  "github.com/shykes/daggerverse/ci",
				kind:     core.ModuleSourceKindGit,
				repoRoot: &vcs.RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
			},
		},
		{
			urlStr: "github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				modPath:  "github.com/shykes/daggerverse.git/ci",
				kind:     core.ModuleSourceKindGit,
				repoRoot: &vcs.RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse.git"},
			},
		},
		// gitlab
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
			want: &parsedRefString{
				modPath:  "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
				kind:     core.ModuleSourceKindGit,
				repoRoot: &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork.git"},
			},
		},
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
			want: &parsedRefString{
				modPath:  "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
				kind:     core.ModuleSourceKindGit,
				repoRoot: &vcs.RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork.git", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
			},
		},
		// bitbucket
		{
			urlStr: "bitbucket.org/test-travail/test/depth1",
			want: &parsedRefString{
				modPath:  "bitbucket.org/test-travail/test/depth1",
				kind:     core.ModuleSourceKindGit,
				repoRoot: &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test", Repo: "https://bitbucket.org/test-travail/test"},
			},
		},
		{
			urlStr: "bitbucket.org/test-travail/test.git/depth1",
			want: &parsedRefString{
				modPath:  "bitbucket.org/test-travail/test.git/depth1",
				kind:     core.ModuleSourceKindGit,
				repoRoot: &vcs.RepoRoot{Root: "bitbucket.org/test-travail/test.git", Repo: "https://bitbucket.org/test-travail/test.git"},
			},
		},
	} {
		tc := tc
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsed := parseRefString(ctx, bkClientDirFalse, tc.urlStr)
			require.NotNil(t, parsed)
			require.Equal(t, parsed.modPath, tc.want.modPath)
			require.Equal(t, parsed.kind, tc.want.kind)
			require.Equal(t, parsed.repoRoot.Repo, tc.want.repoRoot.Repo)
			require.Equal(t, parsed.repoRoot.Root, tc.want.repoRoot.Root)
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
