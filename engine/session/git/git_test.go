package git

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsGitConfigKeyAllowed(t *testing.T) {
	nullChar := "\x00"
	testcases := []struct {
		gitconfig string
		expected  *GitConfig
	}{
		{
			gitconfig: `credential.helper
osxkeychain` + nullChar + `init.defaultbranch
main` + nullChar + `user.name
User Name` + nullChar + `user.email
user-name@gmail.com` + nullChar + `commit.gpgsign
true` + nullChar + `url.ssh://git@github.com/.insteadof
https://github.com/` + nullChar + `core.excludesfile
~/.config/git/.gitignore` + nullChar + `protocol.file.allow
always` + nullChar + `core.repositoryformatversion
0` + nullChar + `core.filemode
true` + nullChar + `core.bare
false` + nullChar + `core.logallrefupdates
true` + nullChar + `core.ignorecase
true` + nullChar + `core.precomposeunicode
true` + nullChar + `remote.origin.url
git@github.com:some-user/some-repo.git` + nullChar + `remote.origin.fetch
+refs/heads/*:refs/remotes/origin/*` + nullChar,
			expected: &GitConfig{
				Entries: []*GitConfigEntry{
					{
						Key:   "url.ssh://git@github.com/.insteadof",
						Value: "https://github.com/",
					},
				},
			},
		},
		{
			gitconfig: `url.insteadof
bar
baz` + nullChar + `credential.helper
osxkeychain` + nullChar + ``,
			expected: &GitConfig{
				Entries: []*GitConfigEntry{
					{
						Key:   "url.insteadof",
						Value: "bar\nbaz",
					},
				},
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.gitconfig, func(t *testing.T) {
			parsed, err := parseGitConfigOutput([]byte(tc.gitconfig))
			require.Nil(t, err)
			require.Equal(t, tc.expected, parsed)
		})
	}
}

// More tests are in ./core/integration/git_test.go
