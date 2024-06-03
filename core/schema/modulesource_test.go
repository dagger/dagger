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

func TestSplitRootAndSubdir(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		// computedRepoRootPath is the path extracted from vcs.computedRepoRootPath
		computedRepoRootPath string
		// userRefPath is the path extracted from user input ref
		userRefPath  string
		expectedRoot string
		expectedSub  string
	}{
		// GitHub test cases
		{
			name:                 "Current ref",
			ref:                  "github.com/shykes/dagger",
			computedRepoRootPath: "/shykes/dagger",
			userRefPath:          "/shykes/dagger",
			expectedRoot:         "shykes/dagger",
			expectedSub:          "",
		},
		{
			name:                 "Current ref with .git suffix",
			ref:                  "github.com/shykes/dagger.git/ci",
			computedRepoRootPath: "/shykes/dagger.git",
			userRefPath:          "/shykes/dagger.git/ci",
			expectedRoot:         "shykes/dagger.git",
			expectedSub:          "ci",
		},
		// GitLab test cases
		{
			name:                 "Other CI, with subdir",
			ref:                  "gitlab.com/grouville-public/subgroup/daggerverse/cargo",
			computedRepoRootPath: "/grouville-public/subgroup/daggerverse.git",
			userRefPath:          "/grouville-public/subgroup/daggerverse/cargo",
			expectedRoot:         "grouville-public/subgroup/daggerverse",
			expectedSub:          "cargo",
		},
		{
			name:                 "Other CI with .git suffix and subdir",
			ref:                  "gitlab.com/grouville-public/subgroup/daggerverse.git/cargo",
			computedRepoRootPath: "/grouville-public/subgroup/daggerverse",
			userRefPath:          "/grouville-public/subgroup/daggerverse.git/cargo",
			expectedRoot:         "grouville-public/subgroup/daggerverse.git",
			expectedSub:          "cargo",
		},
		// vanity URL test cases
		{
			name:                 "Vanity URL where username is present",
			ref:                  "dagger.io/dagger/ci",
			computedRepoRootPath: "/dagger/dagger-go-sdk",
			userRefPath:          "/dagger/ci",
			expectedRoot:         "dagger/dagger-go-sdk",
			expectedSub:          "ci",
		},
		{
			name:                 "Vanity URL where repo is present",
			ref:                  "storj.io/eventkit/deploy",
			computedRepoRootPath: "/storj/eventkit",
			userRefPath:          "/eventkit/deploy",
			expectedRoot:         "storj/eventkit",
			expectedSub:          "deploy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, subdir := splitRootAndSubdir(tt.ref, tt.computedRepoRootPath, tt.userRefPath)
			if root != tt.expectedRoot {
				t.Errorf("expected root %q, got %q", tt.expectedRoot, root)
			}
			if subdir != tt.expectedSub {
				t.Errorf("expected subdir %q, got %q", tt.expectedSub, subdir)
			}
		})
	}
}

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
			parsed, err := parseRefString(ctx, bkClientDirFalse, tc.urlStr)
			require.NoError(t, err)
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
