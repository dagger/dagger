package vcs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractRootAndSubdirFromRef(t *testing.T) {
	tests := []struct {
		name         string
		userRef      string
		vcsRootPath  string
		expectedRoot string
		expectedSub  string
	}{
		// GitHub test cases
		{
			name:         "GitHub ref",
			userRef:      "github.com/shykes/dagger/ci",
			vcsRootPath:  "https://github.com/shykes/dagger",
			expectedRoot: "github.com/shykes/dagger",
			expectedSub:  "ci",
		},
		{
			name:         "Github ref with .git",
			userRef:      "github.com/shykes/dagger.git/ci",
			vcsRootPath:  "https://github.com/shykes/dagger.git",
			expectedRoot: "github.com/shykes/dagger.git",
			expectedSub:  "ci",
		},
		// GitLab test cases
		{
			name:         "Gitlab ref with nested groups",
			userRef:      "gitlab.com/grouville-public/subgroup/daggerverse/cargo",
			vcsRootPath:  "https://gitlab.com/grouville-public/subgroup/daggerverse.git",
			expectedRoot: "gitlab.com/grouville-public/subgroup/daggerverse",
			expectedSub:  "cargo",
		},
		{
			name:         "Gitlab ref with nested groups and .git",
			userRef:      "gitlab.com/grouville-public/subgroup/daggerverse.git/cargo",
			vcsRootPath:  "https://gitlab.com/grouville-public/subgroup/daggerverse",
			expectedRoot: "gitlab.com/grouville-public/subgroup/daggerverse.git",
			expectedSub:  "cargo",
		},
		// Bitbucket test cases
		{
			name:         "Bitbucket ref",
			userRef:      "bitbucket.org/test-travail/test/dossier",
			vcsRootPath:  "https://bitbucket.org/test-travail/test.git",
			expectedRoot: "bitbucket.org/test-travail/test",
			expectedSub:  "dossier",
		},
		{
			name:         "Bitbucket ref with .git",
			userRef:      "bitbucket.org/test-travail/test.git/dossier",
			vcsRootPath:  "https://bitbucket.org/test-travail/test.git",
			expectedRoot: "bitbucket.org/test-travail/test.git",
			expectedSub:  "dossier",
		},
		// // vanity URL test cases
		{
			name:         "Vanity URL where username is present",
			userRef:      "dagger.io/dagger/ci",
			vcsRootPath:  "https://github.com/dagger/dagger-go-sdk",
			expectedRoot: "github.com/dagger/dagger-go-sdk",
			expectedSub:  "ci",
		},
		{
			name:         "Vanity URL where repo is present",
			userRef:      "storj.io/eventkit/deploy",
			vcsRootPath:  "https://github.com/storj/eventkit",
			expectedRoot: "github.com/storj/eventkit",
			expectedSub:  "deploy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, subdir, err := ExtractRootAndSubdirFromRef(tt.userRef, tt.vcsRootPath)
			require.Nil(t, err)
			require.Equal(t, tt.expectedRoot, root)
			require.Equal(t, tt.expectedSub, subdir)
		})
	}
}
