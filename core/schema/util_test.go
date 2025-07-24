package schema

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGetGitIgnoreIncludePaths(t *testing.T) {
	for _, tc := range []struct {
		name       string
		parentPath string
		hostPath   string
		expected   []string
	}{
		{
			name:       "root",
			parentPath: "/",
			hostPath:   "/",
			expected:   []string{"/**/.gitignore"},
		},
		{
			name:       "same root",
			parentPath: "/foo/bar",
			hostPath:   "/foo/bar",
			expected:   []string{"/foo/bar/**/.gitignore"},
		},
		{
			name:       "parent path is a parent directory",
			parentPath: "/foo",
			hostPath:   "/foo/bar/baz",
			expected:   []string{"/foo/.gitignore", "/foo/bar/.gitignore", "/foo/bar/baz/**/.gitignore"},
		},
		{
			name:       "parent path is / and host path is a children directory",
			parentPath: "/",
			hostPath:   "/foo/bar/baz",
			expected:   []string{"/.gitignore", "/foo/.gitignore", "/foo/bar/.gitignore", "/foo/bar/baz/**/.gitignore"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := getGitIgnoreIncludePaths(tc.parentPath, tc.hostPath)
			require.Equal(t, tc.expected, result)
		})
	}
}
