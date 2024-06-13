package vcs

import (
	"context"
	"os"
	"testing"

	"github.com/dagger/dagger/core"
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
		mockClient buildkitClient
		want       *parsedRefString
	}{
		// github
		{
			urlStr: "github.com/shykes/daggerverse/ci",
			want: &parsedRefString{
				ModPath:        "github.com/shykes/daggerverse/ci",
				Kind:           core.ModuleSourceKindGit,
				RepoRoot:       &RepoRoot{Root: "github.com/shykes/daggerverse", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
			},
		},
		{
			urlStr: "github.com/shykes/daggerverse.git/ci",
			want: &parsedRefString{
				ModPath:        "github.com/shykes/daggerverse.git/ci",
				Kind:           core.ModuleSourceKindGit,
				RepoRoot:       &RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "ci",
			},
		},
		{
			urlStr: "github.com/shykes/daggerverse.git/../../",
			want: &parsedRefString{
				ModPath:        "github.com/shykes/daggerverse.git/../../",
				Kind:           core.ModuleSourceKindGit,
				RepoRoot:       &RepoRoot{Root: "github.com/shykes/daggerverse.git", Repo: "https://github.com/shykes/daggerverse"},
				RepoRootSubdir: "../../",
			},
		},
		// gitlab
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
			want: &parsedRefString{
				ModPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork/depth1/depth2",
				Kind:           core.ModuleSourceKindGit,
				RepoRoot:       &RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
				RepoRootSubdir: "depth1/depth2",
			},
		},
		{
			urlStr: "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
			want: &parsedRefString{
				ModPath:        "gitlab.com/testguigui1/dagger-public-sub/mywork.git/depth1/depth2",
				Kind:           core.ModuleSourceKindGit,
				RepoRoot:       &RepoRoot{Root: "gitlab.com/testguigui1/dagger-public-sub/mywork.git", Repo: "https://gitlab.com/testguigui1/dagger-public-sub/mywork"},
				RepoRootSubdir: "depth1/depth2",
			},
		},
		// bitbucket
		{
			urlStr: "bitbucket.org/test-travail/test/depth1",
			want: &parsedRefString{
				ModPath:        "bitbucket.org/test-travail/test/depth1",
				Kind:           core.ModuleSourceKindGit,
				RepoRoot:       &RepoRoot{Root: "bitbucket.org/test-travail/test", Repo: "https://bitbucket.org/test-travail/test"},
				RepoRootSubdir: "depth1",
			},
		},
		{
			urlStr: "bitbucket.org/test-travail/test.git/depth1",
			want: &parsedRefString{
				ModPath:        "bitbucket.org/test-travail/test.git/depth1",
				Kind:           core.ModuleSourceKindGit,
				RepoRoot:       &RepoRoot{Root: "bitbucket.org/test-travail/test.git", Repo: "https://bitbucket.org/test-travail/test"},
				RepoRootSubdir: "depth1",
			},
		},
	} {
		tc := tc
		t.Run(tc.urlStr, func(t *testing.T) {
			t.Parallel()
			parsed := ParseRefStringDir(ctx, bkClientDirFalse, tc.urlStr)
			require.NotNil(t, parsed)
			require.Equal(t, parsed.ModPath, tc.want.ModPath)
			require.Equal(t, parsed.Kind, tc.want.Kind)
			require.Equal(t, parsed.RepoRoot.Repo, tc.want.RepoRoot.Repo)
			require.Equal(t, parsed.RepoRoot.Root, tc.want.RepoRoot.Root)
			require.Equal(t, parsed.RepoRootSubdir, tc.want.RepoRootSubdir)
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
