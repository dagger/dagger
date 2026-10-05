package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitClientKey(t *testing.T) {
	same := []string{
		"github.com/acme/modules/greetings",
		"github.com/acme/modules/greetings@main",
		"github.com/acme/modules/greetings/",
		"https://github.com/acme/modules/greetings@v1.2.3",
		"https://github.com/acme/modules.git/greetings",
		"https://github.com/acme/modules#main:greetings",
		"ssh://git@github.com/acme/modules/greetings",
		"git@github.com:acme/modules/greetings",
		"GitHub.com/acme/modules/greetings",
	}
	for _, address := range same {
		require.Equal(t, GitClientKey(same[0]), GitClientKey(address), address)
	}

	require.Equal(t, GitClientKey("http://10.0.0.5/repo.git/hello"), GitClientKey("http://10.0.0.5/repo.git/hello@main"))
	require.Equal(t, GitClientKey("ssh://git@host:2222/repo.git/hello"), GitClientKey("ssh://host:2222/repo/hello"))

	different := []string{
		"github.com/acme/modules",
		"github.com/acme/modules/provider",
		"github.com/acme/modules/greetings/nested",
		"github.com/acme/other/greetings",
		"github.com/Acme/modules/greetings",
		"gitlab.example.com/acme/modules.git/greetings",
	}
	for _, address := range different {
		require.NotEqual(t, GitClientKey(same[0]), GitClientKey(address), address)
	}
	require.NotEqual(t, GitClientKey("ssh://host:2222/repo/hello"), GitClientKey("ssh://host:2223/repo/hello"))
}

func TestDeclaredGitClients(t *testing.T) {
	ctx := t.Context()
	reader := func(files map[string]string) TreeFileReader {
		return func(rel string) ([]byte, bool, error) {
			data, ok := files[rel]
			return []byte(data), ok, nil
		}
	}
	const rootConfig = `[modules.python-sdk]
source = "github.com/acme/python-sdk"

[sdks.python]
module = "python-sdk"

[sdks.python.scopes."modules/caller"]
is-module = true
clients = ["./modules/lib", "github.com/acme/private/hello@main"]

[sdks.python.scopes."modules/other"]
is-module = true
clients = ["github.com/acme/private/other"]
`

	for _, tc := range []struct {
		name      string
		moduleDir string
		files     map[string]string
		want      []string
	}{
		{
			// A git repository at its commit, or the directory a module was
			// built from: the config sits at the tree's root.
			name:      "config at the root of the tree",
			moduleDir: "modules/caller",
			files:     map[string]string{"dagger.toml": rootConfig},
			want:      []string{"github.com/acme/private/hello@main"},
		},
		{
			// A host repository whose workspace is a subdirectory of it.
			name:      "workspace below the tree's root",
			moduleDir: "ws/.dagger/modules/caller",
			files: map[string]string{"ws/dagger.toml": `[modules.go]
source = "go"

[sdks.go]
module = "go"

[sdks.go.scopes.".dagger/modules/caller"]
is-module = true
clients = ["https://github.com/acme/private.git/hello"]
`},
			want: []string{"https://github.com/acme/private.git/hello"},
		},
		{
			name:      "the nearest config governs",
			moduleDir: "modules/caller",
			files: map[string]string{
				"dagger.toml": rootConfig,
				"modules/caller/dagger.toml": `[modules.go]
source = "go"

[sdks.go]
module = "go"

[sdks.go.scopes."."]
is-module = true
clients = ["github.com/acme/own/client"]
`,
			},
			want: []string{"github.com/acme/own/client"},
		},
		{
			name:      "another scope's clients do not count",
			moduleDir: "modules/unlisted",
			files:     map[string]string{"dagger.toml": rootConfig},
		},
		{
			name:      "no config declares nothing",
			moduleDir: "modules/caller",
			files:     map[string]string{},
		},
		{
			name:      "a config that does not parse declares nothing",
			moduleDir: "modules/caller",
			files:     map[string]string{"dagger.toml": "[sdks.python\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeclaredGitClients(ctx, tc.moduleDir, reader(tc.files))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
