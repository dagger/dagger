package workspace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigWarnings(t *testing.T) {
	data := []byte(`future = true
[modules."custom.sdk"]
source = './sdk'
unknown = 1
[modules."custom.sdk".as-sdk]
name = 'custom'
[modules."custom.sdk".settings]
anything = { nested = [1, 2] }
[sdks.custom]
module = 'custom.sdk'
future = 'keep'
[sdks.custom.scopes.'.']
is-module = true
future = false
[sdks.custom.scopes.'.'.settings]
anything = { nested = true }
[env.test.modules."custom.sdk".settings]
anything = false
[ports.8080]
backendPort = 80
backendService = 'web'
future = 'keep'
`)
	warnings, err := ConfigWarnings(data, "nested/dagger.toml")
	require.NoError(t, err)
	require.Equal(t, []string{
		"nested/dagger.toml:1:1: unsupported field future is ignored",
		"nested/dagger.toml:5:1: unsupported field modules.\"custom.sdk\".as-sdk is ignored; run `dagger ws migrate` to migrate it",
		"nested/dagger.toml:4:1: unsupported field modules.\"custom.sdk\".unknown is ignored",
		"nested/dagger.toml:22:1: unsupported field ports.8080.future is ignored",
		"nested/dagger.toml:11:1: unsupported field sdks.custom.future is ignored",
		"nested/dagger.toml:14:1: unsupported field sdks.custom.scopes.\".\".future is ignored",
	}, warnings)
	cfg, err := ParseConfig(data)
	require.NoError(t, err)
	require.Equal(t, "custom.sdk", cfg.SDKs["custom"].Module)
	updated, err := UpdateConfigBytes(data, cfg)
	require.NoError(t, err)
	require.Equal(t, data, updated)
}

func TestConfigWarningsMatchDecoder(t *testing.T) {
	data := []byte("[modules.provider]\nSOURCE = './sdk'\n[ports.8080]\nbackendservice = 'web'\nbackendPort = 80\n")
	warnings, err := ConfigWarnings(data, "dagger.toml")
	require.NoError(t, err)
	require.Empty(t, warnings)
	cfg, err := ParseConfig(data)
	require.NoError(t, err)
	require.Equal(t, "./sdk", cfg.Modules["provider"].Source)
	require.Equal(t, "web", cfg.Ports["8080"].BackendService)

	warnings, err = ConfigWarnings([]byte("[modules.provider]\nsource = './sdk'\nSOURCE = './ignored'\n"), "dagger.toml")
	require.NoError(t, err)
	require.Equal(t, []string{"dagger.toml:3:1: unsupported field modules.provider.SOURCE is ignored"}, warnings)
}

func TestMigrateConfigBytes(t *testing.T) {
	t.Run("preserves unrelated text and removes SDK marker", func(t *testing.T) {
		original := "# workspace\nignore = [\n  'node_modules', # keep\n]\nfuture = true\n\n[modules.dagger-go-sdk]\nsource = './sdk' # keep\npin = 'abc'\n\n[modules.dagger-go-sdk.as-sdk]\nname = 'golang'\n"
		updated, err := MigrateConfigBytes([]byte(original), ".")
		require.NoError(t, err)
		require.Equal(t, strings.Replace(original, "[modules.dagger-go-sdk.as-sdk]\nname = 'golang'\n", "", 1)+"\n[sdks.golang]\nmodule = \"dagger-go-sdk\"\n", string(updated))
		again, err := MigrateConfigBytes(updated, ".")
		require.NoError(t, err)
		require.Equal(t, updated, again)
	})

	t.Run("managed modules clients options and pins", func(t *testing.T) {
		data := []byte(`[modules.provider]
source = './sdk'
[modules.provider.as-sdk]
name = 'custom'
[[modules.provider.as-sdk.modules]]
path = '/api'
[[modules.provider.as-sdk.clients]]
path = '/api'
module = '../target'
package = 'bindings'
[[modules.provider.as-sdk.clients]]
path = './client'
module = 'ssh://git@github.com/org/repo@main'
pin = '0123456789abcdef'
[sdks.custom]
module = 'provider'
future = 'keep'
[sdks.custom.scopes.'/api']
name = 'original'
clients = ['existing']
[sdks.custom.scopes.'/api'.settings]
package = 'bindings'
`)
		updated, err := MigrateConfigBytes(data, "nested")
		require.NoError(t, err)
		require.NotContains(t, string(updated), "as-sdk")
		require.Contains(t, string(updated), "future = 'keep'")
		cfg, err := ParseConfig(updated)
		require.NoError(t, err)
		require.Equal(t, SDKScope{IsModule: true, Name: "original", Clients: []string{"existing", "../target"}, Settings: map[string]any{"package": "bindings"}}, cfg.SDKs["custom"].Scopes["/api"])
		require.Equal(t, []string{"ssh://git@github.com/org/repo@0123456789abcdef"}, cfg.SDKs["custom"].Scopes["./client"].Clients)
		again, err := MigrateConfigBytes(updated, "nested")
		require.NoError(t, err)
		require.Equal(t, updated, again)
	})

	t.Run("inline and quoted SDK markers", func(t *testing.T) {
		data := []byte("modules.'dagger-custom-sdk' = { source = './sdk', as-sdk = { modules = [{path = '.'}], clients = [] }, future = true }\r\n")
		updated, err := MigrateConfigBytes(data, ".")
		require.NoError(t, err)
		require.NotContains(t, string(updated), "as-sdk")
		require.Contains(t, string(updated), "future = true")
		require.NotContains(t, strings.ReplaceAll(string(updated), "\r\n", ""), "\n")
		cfg, err := ParseConfig(updated)
		require.NoError(t, err)
		require.Equal(t, "dagger-custom-sdk", cfg.SDKs["custom"].Module)
		require.True(t, cfg.SDKs["custom"].Scopes["."].IsModule)
	})

	t.Run("current config makes no change", func(t *testing.T) {
		data := []byte("# keep\nfuture = [ 1, 2 ]\n[modules]\n")
		updated, err := MigrateConfigBytes(data, ".")
		require.NoError(t, err)
		require.Equal(t, data, updated)
	})
}

func TestMigrateConfigConflicts(t *testing.T) {
	for _, tc := range []struct{ name, legacy, extra, want string }{
		{"provider", "name = 'custom'", "[modules.other]\nsource = './other'\n[sdks.custom]\nmodule = 'other'", "already uses module"},
		{"SDK name", "name = 'custom'", "[sdks.other]\nmodule = 'provider'", "legacy SDK name"},
		{"unknown legacy field", "future = true", "", "unsupported legacy field"},
		{"unknown module field", "[[modules.provider.as-sdk.modules]]\npath = '.'\nfuture = true", "", "unsupported legacy field"},
		{"unknown client data", "[[modules.provider.as-sdk.clients]]\npath = '.'\nmodule = '.'\nfuture = true", "", "unsupported legacy client field"},
		{"missing module path", "[[modules.provider.as-sdk.modules]]", "", "path must be a non-empty string"},
		{"invalid module list", "modules = ['.']", "", "must be an array of tables"},
		{"escaping scope", "[[modules.provider.as-sdk.modules]]\npath = '../outside'", "", "escapes the workspace root"},
		{"local pin", "[[modules.provider.as-sdk.clients]]\npath = '.'\nmodule = './local'\npin = 'abc'", "", "local client target has a pin"},
		{"scope module flag", "[[modules.provider.as-sdk.modules]]\npath = '.'", "[sdks.provider]\nmodule = 'provider'\n[sdks.provider.scopes.'.']\nis-module = false", "explicitly sets is-module = false"},
		{"settings", "[[modules.provider.as-sdk.clients]]\npath = '.'\nmodule = '.'\npackage = 'new'", "[sdks.provider]\nmodule = 'provider'\n[sdks.provider.scopes.'.'.settings]\npackage = 'old'", "conflicts in settings.package"},
		{"environment", "", "[env.test.modules.provider.as-sdk]\nname = 'test'", "environment-specific SDK roles"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("[modules.provider]\nsource = './sdk'\n[modules.provider.as-sdk]\n" + tc.legacy + "\n" + tc.extra + "\n")
			original := string(data)
			updated, err := MigrateConfigBytes(data, ".")
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, updated)
			require.Equal(t, original, string(data))
		})
	}
}
