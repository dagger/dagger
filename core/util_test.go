package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGitIgnore(t *testing.T) {
	t.Run("absolute paths", func(t *testing.T) {
		gitIgnoreContent := `foo
bar
**.go
# comment


**/foo`

		patterns := parseGitIgnore(
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

		patterns := parseGitIgnore(
			gitIgnoreContent, ".",
		)

		require.Equal(t, []string{"foo/bar", "bar", "baz", "*.go"}, patterns)
	})

	t.Run("negative patterns", func(t *testing.T) {
		gitIgnoreContent := `!bar
!baz/foo/x`

		patterns := parseGitIgnore(
			gitIgnoreContent, ".",
		)

		require.Equal(t, []string{"!**/bar", "!baz/foo/x"}, patterns)
	})

	t.Run("dir only exclusion", func(t *testing.T) {
		gitIgnoreContent := `foo/
!build*/
./node_modules/
`

		patterns := parseGitIgnore(
			gitIgnoreContent, ".",
		)

		require.Equal(t, []string{"**/foo/**", "!**/build*/**", "node_modules/**"}, patterns)
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

		patterns := parseGitIgnore(
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
