package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
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
			expected:   []string{"**/.gitignore"},
		},
		{
			name:       "same root",
			parentPath: "/foo/bar",
			hostPath:   "/foo/bar",
			expected:   []string{"**/.gitignore"},
		},
		{
			name:       "parent path is a parent directory",
			parentPath: "/foo",
			hostPath:   "/foo/bar/baz",
			expected:   []string{"bar/.gitignore", ".gitignore", "bar/baz/**/.gitignore"},
		},
		{
			name:       "parent path is / and host path is a children directory",
			parentPath: "/",
			hostPath:   "/foo/bar/baz",
			expected:   []string{"foo/bar/.gitignore", "foo/.gitignore", ".gitignore", "foo/bar/baz/**/.gitignore"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := getGitIgnoreIncludePaths(tc.parentPath, tc.hostPath)
			require.NoError(t, err)
			require.Equal(t, tc.expected, result)
		})
	}
}
