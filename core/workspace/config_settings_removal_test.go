package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateConfigRemovesTableSettings(t *testing.T) {
	for _, location := range []struct {
		name, prefix string
		settings     func(*Config) map[string]any
	}{
		{"module", "modules.foo", func(cfg *Config) map[string]any { return cfg.Modules["foo"].Settings }},
		{"environment", "env.ci.modules.foo", func(cfg *Config) map[string]any { return cfg.Env["ci"].Modules["foo"].Settings }},
		{"sdk", "sdks.test.scopes.demo", func(cfg *Config) map[string]any { return cfg.SDKs["test"].Scopes["demo"].Settings }},
	} {
		for _, replacement := range []string{"remove", "remove with sibling", "empty object"} {
			t.Run(location.name+"/"+replacement, func(t *testing.T) {
				input := "[modules.foo]\nsource = './foo'\nfuture = 'keep'\n\n[sdks.test]\nmodule = 'foo'\n\n[" + location.prefix + ".settings.options.nested]\nenabled = true\n"
				if replacement == "remove with sibling" {
					input += "\n[" + location.prefix + ".settings]\nsibling = 'keep'\n"
				}
				cfg, err := ParseConfig([]byte(input))
				require.NoError(t, err)
				settings := location.settings(cfg)
				if replacement != "empty object" {
					delete(settings, "options")
				} else {
					settings["options"] = map[string]any{}
				}
				out, err := UpdateConfigBytes([]byte(input), cfg)
				require.NoError(t, err)
				parsed, err := ParseConfig(out)
				require.NoError(t, err)
				if replacement != "empty object" {
					require.NotContains(t, location.settings(parsed), "options")
				} else {
					require.Equal(t, map[string]any{}, location.settings(parsed)["options"])
				}
				if replacement == "remove with sibling" {
					require.Equal(t, "keep", location.settings(parsed)["sibling"])
				}
				require.Contains(t, string(out), "future = 'keep'")
			})
		}
	}
}
