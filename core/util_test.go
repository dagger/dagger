package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractGitIgnorePatterns(t *testing.T) {
	t.Run("absolute paths", func(t *testing.T) {
		gitIgnoreContent := `foo
bar
**.go
# comment


**/foo`

		patterns := extractGitIgnorePatterns(
			gitIgnoreContent, ".",
		)

		require.Equal(t, []string{"**/foo", "**/bar", "**.go", "**/foo"}, patterns)
	})

	t.Run("relative path", func(t *testing.T) {
		gitIgnoreContent := `# comment to ignore
foo/bar

/bar

./baz

# empty line to verify its ignored
*.go
`

		patterns := extractGitIgnorePatterns(
			gitIgnoreContent, ".",
		)

		require.Equal(t, []string{"foo/bar", "bar", "baz", "*.go"}, patterns)
	})

	t.Run("negative patterns", func(t *testing.T) {
		gitIgnoreContent := `!bar
!baz/foo/x`

		patterns := extractGitIgnorePatterns(
			gitIgnoreContent, ".",
		)

		require.Equal(t, []string{"!**/bar", "!baz/foo/x"}, patterns)
	})

	t.Run("parent dir setting", func(t *testing.T) {
		gitIgnoreContent := `**.go
.gitignore
foo/bar/baz
!test
# comment
hello/world/**/baz
/foo
`

		patterns := extractGitIgnorePatterns(
			gitIgnoreContent, "/parent/subdir",
		)

		require.Equal(t, []string{
			"/parent/subdir/**.go",
			"/parent/subdir/**/.gitignore",
			"/parent/subdir/foo/bar/baz",
			"!/parent/subdir/**/test",
			"/parent/subdir/hello/world/**/baz",
			"/parent/subdir/foo",
		}, patterns)
	})
}
