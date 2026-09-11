package workspace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const preservationConfig = `# Workspace notes
ignore = [
    'dist', # generated output
    "node_modules",
]
defaults_from_dotenv = false
future = { enabled = true }

[modules."my.module"] # module notes
source = './module'
entrypoint = false # keep this comment
pin = 'abc123'
future = [1, 2]

[modules."my.module".as-sdk]
name = 'legacy'

[modules."my.module".settings]
"some.key" = 'old' # setting notes
empty = []

[sdks.go]
module = 'my.module'

[sdks.go.scopes."."]
is-module = false
clients = [
  'one', # client notes
  'two',
]

[ports.3000]
backendService = 'web'
backendPort = 8080

# Keep this environment
[env.local]
`

func TestConfigPreservation(t *testing.T) {
	t.Parallel()

	t.Run("unchanged config is byte identical", func(t *testing.T) {
		cfg, err := ParseConfig([]byte(preservationConfig))
		require.NoError(t, err)
		out, err := UpdateConfigBytes([]byte(preservationConfig), cfg)
		require.NoError(t, err)
		require.Equal(t, preservationConfig, string(out))
	})

	t.Run("set one value", func(t *testing.T) {
		out, err := WriteConfigValue([]byte(preservationConfig), `modules."my.module".entrypoint`, "true")
		require.NoError(t, err)
		want := strings.Replace(preservationConfig, "entrypoint = false", "entrypoint = true", 1)
		require.Equal(t, want, string(out))
		again, err := WriteConfigValue(out, `modules."my.module".entrypoint`, "true")
		require.NoError(t, err)
		require.Equal(t, out, again)
	})

	t.Run("quoted setting keeps quotes and comment", func(t *testing.T) {
		out, err := WriteConfigValue([]byte(preservationConfig), `modules."my.module".settings."some.key"`, "new")
		require.NoError(t, err)
		require.Equal(t, strings.Replace(preservationConfig, "'old'", "'new'", 1), string(out))
	})

	t.Run("unset one value", func(t *testing.T) {
		out, err := DeleteConfigValue([]byte(preservationConfig), `modules."my.module".entrypoint`)
		require.NoError(t, err)
		require.Equal(t, strings.Replace(preservationConfig, "entrypoint = false # keep this comment\n", "", 1), string(out))
	})

	t.Run("unset last setting removes its section and separator", func(t *testing.T) {
		out, err := DeleteConfigValue([]byte(preservationConfig), `modules."my.module".settings."some.key"`)
		require.NoError(t, err)
		out, err = DeleteConfigValue(out, `modules."my.module".settings.empty`)
		require.NoError(t, err)
		require.Equal(t, strings.Replace(preservationConfig, "[modules.\"my.module\".settings]\n\"some.key\" = 'old' # setting notes\nempty = []\n\n", "", 1), string(out))
	})

	t.Run("SDK edit preserves other declarations", func(t *testing.T) {
		cfg, err := ParseConfig([]byte(preservationConfig))
		require.NoError(t, err)
		sdk := cfg.SDKs["go"]
		scope := sdk.Scopes["."]
		scope.IsModule = true
		sdk.Scopes["."] = scope
		cfg.SDKs["go"] = sdk
		out, err := UpdateConfigBytes([]byte(preservationConfig), cfg)
		require.NoError(t, err)
		require.Equal(t, strings.Replace(preservationConfig, "is-module = false", "is-module = true", 1), string(out))
	})

	t.Run("numeric key edit", func(t *testing.T) {
		cfg, err := ParseConfig([]byte(preservationConfig))
		require.NoError(t, err)
		port := cfg.Ports["3000"]
		port.BackendPort = 9090
		cfg.Ports["3000"] = port
		out, err := UpdateConfigBytes([]byte(preservationConfig), cfg)
		require.NoError(t, err)
		require.Equal(t, strings.Replace(preservationConfig, "8080", "9090", 1), string(out))
	})

	t.Run("line endings and missing final newline", func(t *testing.T) {
		input := strings.ReplaceAll(strings.TrimSuffix(preservationConfig, "\n"), "\n", "\r\n")
		out, err := WriteConfigValue([]byte(input), `modules."my.module".entrypoint`, "true")
		require.NoError(t, err)
		require.Equal(t, strings.Replace(input, "entrypoint = false", "entrypoint = true", 1), string(out))
	})

	t.Run("explicit default is written", func(t *testing.T) {
		input := "# empty workspace\n"
		out, err := WriteConfigValue([]byte(input), "defaults_from_dotenv", "false")
		require.NoError(t, err)
		require.Equal(t, input+"defaults_from_dotenv = false\n", string(out))
	})

	t.Run("changed array keeps layout and comments", func(t *testing.T) {
		out, err := WriteConfigValues([]byte(preservationConfig), "ignore", []string{"dist", "vendor"})
		require.NoError(t, err)
		require.Equal(t, strings.Replace(preservationConfig, `"node_modules"`, `"vendor"`, 1), string(out))
	})

	t.Run("adding a module preserves existing text", func(t *testing.T) {
		cfg, err := ParseConfig([]byte(preservationConfig))
		require.NoError(t, err)
		cfg.Modules["new"] = ModuleEntry{Source: "./new"}
		out, err := UpdateConfigBytes([]byte(preservationConfig), cfg)
		require.NoError(t, err)
		require.Equal(t, preservationConfig+"\n[modules.new]\nsource = \"./new\"\n", string(out))
		parsed, err := ParseConfig(out)
		require.NoError(t, err)
		require.Equal(t, cfg, parsed)
	})

	t.Run("removal includes unknown fields in removed module", func(t *testing.T) {
		input := "# keep\n[modules.old]\nsource = './old'\nfuture = true\n[modules.old.as-sdk]\nname = 'old'\n\n[modules.kept]\nsource = './kept'\nfuture = 'kept'\n"
		cfg, err := ParseConfig([]byte(input))
		require.NoError(t, err)
		delete(cfg.Modules, "old")
		out, err := UpdateConfigBytes([]byte(input), cfg)
		require.NoError(t, err)
		require.Equal(t, "# keep\n\n[modules.kept]\nsource = './kept'\nfuture = 'kept'\n", string(out))
	})
}

func TestConfigPreservationSyntax(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input, key, value, want string
		remove                        bool
	}{
		{
			name:  "dotted module at root",
			input: "modules.foo.source = './foo'\n# keep\n",
			key:   "modules.foo.entrypoint", value: "true",
			want: "modules.foo.source = './foo'\nmodules.foo.entrypoint = true\n# keep\n",
		},
		{
			name:  "dotted module in table",
			input: "[modules]\nfoo.source = './foo'\n",
			key:   "modules.foo.entrypoint", value: "true",
			want: "[modules]\nfoo.source = './foo'\nfoo.entrypoint = true\n",
		},
		{
			name:  "inline module",
			input: "[modules]\nfoo = {source = './foo', entrypoint = false} # keep\n",
			key:   "modules.foo.entrypoint", value: "true",
			want: "[modules]\nfoo = {source = './foo', entrypoint = true} # keep\n",
		},
		{
			name:  "nested inline addition",
			input: "modules = {foo = {source = './foo'}}\n",
			key:   "modules.foo.entrypoint", value: "true",
			want: "modules = {foo = {source = './foo', entrypoint = true}}\n",
		},
		{
			name:  "inline removal",
			input: "[modules]\nfoo = {source = './foo', entrypoint = false}\n",
			key:   "modules.foo.entrypoint", remove: true,
			want: "[modules]\nfoo = {source = './foo'}\n",
		},
		{
			name:  "whitespace in quoted path",
			input: "[ modules . 'my.module' ]\nsource = './foo'\nentrypoint = false\n",
			key:   `modules."my.module".entrypoint`, value: "true",
			want: "[ modules . 'my.module' ]\nsource = './foo'\nentrypoint = true\n",
		},
		{
			name:  "unknown array of tables",
			input: "defaults_from_dotenv = false\n[[future]]\nvalue = 1\n[[future]]\nvalue = 2\n",
			key:   "defaults_from_dotenv", value: "true",
			want: "defaults_from_dotenv = true\n[[future]]\nvalue = 1\n[[future]]\nvalue = 2\n",
		},
		{
			name:  "multiline string with config syntax",
			input: "[modules.foo]\nsource = './foo'\n[modules.foo.settings]\ntext = '''\n[not-a-table]\nkey = 'value'\n'''\nenabled = false\n",
			key:   "modules.foo.settings.enabled", value: "true",
			want: "[modules.foo]\nsource = './foo'\n[modules.foo.settings]\ntext = '''\n[not-a-table]\nkey = 'value'\n'''\nenabled = true\n",
		},
		{
			name:  "replace expanded table with scalar",
			input: "[modules.foo]\nsource = './foo'\n[modules.foo.settings.value]\nnested = true\n",
			key:   "modules.foo.settings.value", value: "replacement",
			want: "[modules.foo]\nsource = './foo'\n\n[modules.foo.settings]\nvalue = \"replacement\"\n",
		},
		{
			name:  "replace dotted table with scalar",
			input: "[modules.foo]\nsource = './foo'\nsettings.value.nested = true\n",
			key:   "modules.foo.settings.value", value: "replacement",
			want: "[modules.foo]\nsource = './foo'\n\n[modules.foo.settings]\nvalue = \"replacement\"\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out []byte
			var err error
			if tc.remove {
				out, err = DeleteConfigValue([]byte(tc.input), tc.key)
			} else {
				out, err = WriteConfigValue([]byte(tc.input), tc.key, tc.value)
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, string(out))
		})
	}
}

func TestConfigPreservationArrays(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, array string
		value       []string
	}{
		{"append", `['one']`, []string{"one", "two"}},
		{"append with trailing comma", `['one',]`, []string{"one", "two"}},
		{"append to empty", `[]`, []string{"one"}},
		{"append to empty multiline", "[\n]", []string{"one"}},
		{"append with comment", "[\n 'one' # keep\n]", []string{"one", "two"}},
		{"append with closing bracket on same line", "[\n 'one']", []string{"one", "two"}},
		{"remove all", `['one', 'two']`, []string{}},
		{"remove with comment", "[\n 'one', # keep\n 'two',\n]", []string{"one"}},
		{"remove with comment before comma", "[\n 'one' # keep\n , 'two',\n]", []string{}},
		{"replace and append", `['one']`, []string{"new", "two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "# before\nignore = " + tc.array + " # after\nfuture = 'keep'\n"
			out, err := WriteConfigValues([]byte(input), "ignore", tc.value)
			require.NoError(t, err)
			parsed, err := ParseConfig(out)
			require.NoError(t, err)
			require.Equal(t, tc.value, parsed.Ignore)
			require.True(t, strings.HasPrefix(string(out), "# before\nignore = "))
			require.True(t, strings.HasSuffix(string(out), " # after\nfuture = 'keep'\n"))
			if strings.Contains(tc.array, "# keep") {
				require.Contains(t, string(out), "# keep")
			}
			if strings.Contains(tc.array, "\n") {
				require.Contains(t, string(out), "ignore = [\n")
			}
			again, err := WriteConfigValues(out, "ignore", tc.value)
			require.NoError(t, err)
			require.Equal(t, out, again)
		})
	}
}

func TestConfigPreservationUnknownSibling(t *testing.T) {
	input := "[modules.foo]\nsource = './foo'\n[modules.foo.check]\nskip = ['slow']\nfuture = 'keep'\n"
	cfg, err := ParseConfig([]byte(input))
	require.NoError(t, err)
	entry := cfg.Modules["foo"]
	entry.Check.Skip = nil
	cfg.Modules["foo"] = entry
	out, err := UpdateConfigBytes([]byte(input), cfg)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(input, "skip = ['slow']\n", "", 1), string(out))
}

func TestConfigPreservationInlineRemoval(t *testing.T) {
	input := "modules = {old.source = './old', old.future = true, kept.source = './kept'} # keep\n"
	cfg, err := ParseConfig([]byte(input))
	require.NoError(t, err)
	delete(cfg.Modules, "old")
	out, err := UpdateConfigBytes([]byte(input), cfg)
	require.NoError(t, err)
	require.Equal(t, "modules = {  kept.source = './kept'} # keep\n", string(out))
	parsed, err := ParseConfig(out)
	require.NoError(t, err)
	require.Equal(t, cfg, parsed)
}

func TestConfigPreservationStringStyles(t *testing.T) {
	for _, tc := range []struct{ old, value, want string }{
		{"'old'", "new", "'new'"},
		{"'''\nold'''", "new\nvalue", "'''\nnew\nvalue'''"},
		{"\"\"\"\nold\"\"\"", "new\nvalue", "\"\"\"\nnew\nvalue\"\"\""},
		{"'old'", "quote'value", "\"quote'value\""},
		{"'old'", "control\x1f", "\"control\\u001f\""},
		{"'''old'''", "\nnew", "\"\\nnew\""},
		{"\"\"\"old\"\"\"", "\nnew", "\"\\nnew\""},
	} {
		t.Run(tc.old+tc.value, func(t *testing.T) {
			input := "[modules.foo]\nsource = './foo'\n[modules.foo.settings]\ntext = " + tc.old + " # keep\n"
			out, err := WriteConfigValue([]byte(input), "modules.foo.settings.text", tc.value)
			require.NoError(t, err)
			require.Equal(t, strings.Replace(input, tc.old, tc.want, 1), string(out))
			cfg, err := ParseConfig(out)
			require.NoError(t, err)
			require.Equal(t, tc.value, cfg.Modules["foo"].Settings["text"])
		})
	}
}

func FuzzConfigPreservation(f *testing.F) {
	for _, input := range []string{
		preservationConfig,
		"defaults_from_dotenv = false # final comment",
		"[modules.foo]\nsource = './foo'\n[modules.foo.settings]\nvalue = +nan\n",
		"modules = {foo = {source = './foo'}}\n",
		"future = [{key = 'value'}]\n",
	} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		cfg, err := ParseConfig([]byte(input))
		if err != nil {
			return
		}
		out, err := UpdateConfigBytes([]byte(input), cfg)
		require.NoError(t, err)
		require.Equal(t, input, string(out))
		// Editing a supported root field must either leave the input alone
		// with an error, or produce a valid config with the requested value.
		out, err = WriteConfigValue([]byte(input), "defaults_from_dotenv", "true")
		if err != nil {
			require.Nil(t, out)
			return
		}
		changed, err := ParseConfig(out)
		require.NoError(t, err)
		require.True(t, changed.DefaultsFromDotEnv)
	})
}

func TestConfigPreservationInlineEnvironment(t *testing.T) {
	input := "modules = {foo = {source = './foo'}}\nenv = {ci = {modules = {foo = {settings = {value = 'old'}}}}}\n"
	out, err := DeleteConfigValue([]byte(input), "env.ci.modules.foo.settings.value")
	require.NoError(t, err)
	require.Equal(t, "modules = {foo = {source = './foo'}}\nenv = {ci = {}}\n", string(out))
	cfg, err := ParseConfig(out)
	require.NoError(t, err)
	require.Contains(t, cfg.Env, "ci")
}
