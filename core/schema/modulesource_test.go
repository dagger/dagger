package schema

import (
	"testing"
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
			ref:                  "github.com/shykes/dagger.git",
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
			name:                 "Ref with .git suffix",
			ref:                  "dagger.io/dagger/ci",
			computedRepoRootPath: "/dagger/dagger-go-sdk",
			userRefPath:          "/dagger/ci",
			expectedRoot:         "dagger/dagger-go-sdk",
			expectedSub:          "ci",
		},
		{
			name:                 "Ref with .git suffix",
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
