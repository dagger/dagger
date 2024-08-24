package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitModuleSourceSymbolic(t *testing.T) {
	testCases := []struct {
		name        string
		cloneRef    string
		rootSubpath string
		expected    string
	}{
		{
			name:        "Go-style URL",
			cloneRef:    "https://github.com/user/repo.git",
			rootSubpath: "subdir",
			expected:    "https://github.com/user/repo.git/subdir",
		},
		{
			name:        "SCP-like reference",
			cloneRef:    "git@github.com:user/repo.git",
			rootSubpath: "subdir",
			expected:    "git@github.com:user/repo.git/subdir",
		},
		{
			name:        "SCP-like reference with no subdir",
			cloneRef:    "git@github.com:user/repo.git",
			rootSubpath: "",
			expected:    "git@github.com:user/repo.git",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			src := &GitModuleSource{
				CloneRef:    tc.cloneRef,
				RootSubpath: tc.rootSubpath,
			}
			result := src.Symbolic()
			require.Equal(t, tc.expected, result, "Symbolic() returned unexpected result")
		})
	}
}
