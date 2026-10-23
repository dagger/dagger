package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainName(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
		want string
		err  string
	}{
		{name: "no config"},
		{name: "none", cfg: &Config{Modules: map[string]ModuleEntry{"tools": {Source: "./tools"}}}},
		{name: "installed name", cfg: &Config{Modules: map[string]ModuleEntry{"tool.box": {Source: "./tools", Main: true}}}, want: "tool.box"},
		{name: "ambiguous", cfg: &Config{Modules: map[string]ModuleEntry{"z": {Main: true}, "a": {Main: true}}}, err: "multiple workspace main modules: [\"a\" \"z\"]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, err := MainName(tc.cfg)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, name)
		})
	}
}

func TestSetMain(t *testing.T) {
	original := []byte(`# keep
ignore = [
  'node_modules', # keep
]
future = true
[modules.old]
source = './old'
main = true
[modules.'tool.box']
source = './tools'
main = false
[modules.'tool.box'.as-sdk]
name = 'custom'
[env.test.modules.old.settings]
arbitrary = 'keep'
`)
	cfg, err := ParseConfig(original)
	require.NoError(t, err)
	for _, name := range []string{"missing", "./tools", "tool.box@v2"} {
		before := cloneConfig(cfg)
		require.ErrorContains(t, SetMain(cfg, ".", name), "is not installed")
		require.Equal(t, before, cfg, "a rejected name must not change any flags")
	}
	require.NoError(t, SetMain(cfg, ".", "tool.box"))
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
main = true
[modules.'tool.box'.as-sdk]
name = 'custom'
[env.test.modules.old.settings]
arbitrary = 'keep'
`, string(updated))
	require.NoError(t, SetMain(cfg, ".", "tool.box"))
	again, err := UpdateConfigBytes(updated, cfg)
	require.NoError(t, err)
	require.Equal(t, updated, again)
	require.NoError(t, SetMain(cfg, ".", ""))
	name, err := MainName(cfg)
	require.NoError(t, err)
	require.Empty(t, name)
}

func TestSetMainRepairsAmbiguousConfig(t *testing.T) {
	cfg := &Config{Modules: map[string]ModuleEntry{
		"a": {Source: "./a", Main: true},
		"b": {Source: "./b", Main: true},
		"c": {Source: "./c"},
	}}
	require.NoError(t, SetMain(cfg, ".", "c"))
	name, err := MainName(cfg)
	require.NoError(t, err)
	require.Equal(t, "c", name)
	require.False(t, cfg.Modules["a"].Main)
	require.False(t, cfg.Modules["b"].Main)
}

func TestSetMainPreservesScopeNames(t *testing.T) {
	for _, next := range []string{"", "other", "demo-dev"} {
		t.Run("select="+next, func(t *testing.T) {
			cfg := &Config{
				Modules: map[string]ModuleEntry{
					"demo-dev": {Source: "../generated/api", Main: true},
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
			require.NoError(t, SetMain(cfg, "project", next))
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

func TestPreserveInferredScopeNames(t *testing.T) {
	// A migrated root module: the scope carries no name and takes it from the
	// installed main, whose source resolves to the same directory.
	base := func() *Config {
		return &Config{
			Modules: map[string]ModuleEntry{
				"myapp": {Source: ".", Main: true},
				"lib":   {Source: "libs/lib", Main: true},
			},
			SDKs: map[string]SDKEntry{
				"go": {Scopes: map[string]SDKScope{
					".":        {IsModule: true},
					"libs/lib": {IsModule: true},
				}},
			},
		}
	}

	t.Run("records the name of the detached main only", func(t *testing.T) {
		cfg := base()
		require.NoError(t, PreserveInferredScopeNames(cfg, ".", func(installed string) bool {
			return installed == "myapp"
		}))
		require.Equal(t, map[string]SDKScope{
			".":        {IsModule: true, Name: "myapp"},
			"libs/lib": {IsModule: true},
		}, cfg.SDKs["go"].Scopes)
		require.Equal(t, base().Modules, cfg.Modules, "installs are left to the caller")
	})

	t.Run("an ambiguous inferred name is not guessed", func(t *testing.T) {
		cfg := base()
		cfg.Modules["twin"] = ModuleEntry{Source: "./", Main: true}
		require.NoError(t, PreserveInferredScopeNames(cfg, ".", func(installed string) bool {
			return installed == "myapp"
		}))
		require.Empty(t, cfg.SDKs["go"].Scopes["."].Name)
	})
}
