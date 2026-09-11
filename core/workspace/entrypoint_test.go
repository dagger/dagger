package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEntrypointName(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
		want string
		err  string
	}{
		{name: "no config"},
		{name: "none", cfg: &Config{Modules: map[string]ModuleEntry{"tools": {Source: "./tools"}}}},
		{name: "installed name", cfg: &Config{Modules: map[string]ModuleEntry{"tool.box": {Source: "./tools", Entrypoint: true}}}, want: "tool.box"},
		{name: "ambiguous", cfg: &Config{Modules: map[string]ModuleEntry{"z": {Entrypoint: true}, "a": {Entrypoint: true}}}, err: "multiple entrypoint modules: [\"a\" \"z\"]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, err := EntrypointName(tc.cfg)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, name)
		})
	}
}

func TestSetEntrypoint(t *testing.T) {
	original := []byte(`# keep
ignore = [
  'node_modules', # keep
]
future = true
[modules.old]
source = './old'
entrypoint = true
[modules.'tool.box']
source = './tools'
entrypoint = false
[modules.'tool.box'.as-sdk]
name = 'custom'
[env.test.modules.old.settings]
arbitrary = 'keep'
`)
	cfg, err := ParseConfig(original)
	require.NoError(t, err)
	for _, name := range []string{"missing", "./tools", "tool.box@v2"} {
		before := cloneConfig(cfg)
		require.ErrorContains(t, SetEntrypoint(cfg, ".", name), "is not installed")
		require.Equal(t, before, cfg, "a rejected name must not change any flags")
	}
	require.NoError(t, SetEntrypoint(cfg, ".", "tool.box"))
	updated, err := UpdateConfigBytes(original, cfg)
	require.NoError(t, err)
	require.Equal(t, `# keep
ignore = [
  'node_modules', # keep
]
future = true
[modules.old]
source = './old'
[modules.'tool.box']
source = './tools'
entrypoint = true
[modules.'tool.box'.as-sdk]
name = 'custom'
[env.test.modules.old.settings]
arbitrary = 'keep'
`, string(updated))
	require.NoError(t, SetEntrypoint(cfg, ".", "tool.box"))
	again, err := UpdateConfigBytes(updated, cfg)
	require.NoError(t, err)
	require.Equal(t, updated, again)
	require.NoError(t, SetEntrypoint(cfg, ".", ""))
	name, err := EntrypointName(cfg)
	require.NoError(t, err)
	require.Empty(t, name)
}

func TestSetEntrypointRepairsAmbiguousConfig(t *testing.T) {
	cfg := &Config{Modules: map[string]ModuleEntry{
		"a": {Source: "./a", Entrypoint: true},
		"b": {Source: "./b", Entrypoint: true},
		"c": {Source: "./c"},
	}}
	require.NoError(t, SetEntrypoint(cfg, ".", "c"))
	name, err := EntrypointName(cfg)
	require.NoError(t, err)
	require.Equal(t, "c", name)
	require.False(t, cfg.Modules["a"].Entrypoint)
	require.False(t, cfg.Modules["b"].Entrypoint)
}

func TestSetEntrypointPreservesScopeNames(t *testing.T) {
	for _, next := range []string{"", "other", "demo-dev"} {
		t.Run("select="+next, func(t *testing.T) {
			cfg := &Config{
				Modules: map[string]ModuleEntry{
					"demo-dev": {Source: "../generated/api", Entrypoint: true},
					"other":    {Source: "other"},
				},
				SDKs: map[string]SDKEntry{
					"test": {Scopes: map[string]SDKScope{
						"/generated/api":   {IsModule: true},
						"../generated/api": {IsModule: true, Name: "explicit"},
						"other":            {IsModule: true},
					}},
					"client": {Scopes: map[string]SDKScope{"../generated/api": {}}},
				},
			}
			require.NoError(t, SetEntrypoint(cfg, "project", next))
			want := "demo-dev"
			if next == "demo-dev" {
				want = ""
			}
			require.Equal(t, want, cfg.SDKs["test"].Scopes["/generated/api"].Name)
			require.Equal(t, "explicit", cfg.SDKs["test"].Scopes["../generated/api"].Name)
			require.Empty(t, cfg.SDKs["test"].Scopes["other"].Name)
			require.Empty(t, cfg.SDKs["client"].Scopes["../generated/api"].Name)
		})
	}
}
