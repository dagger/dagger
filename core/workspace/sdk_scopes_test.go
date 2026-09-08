package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReconcileSDKScopes(t *testing.T) {
	for _, test := range []struct {
		name      string
		configDir string
		keys      []string
		resolved  string
	}{
		{name: "relative", configDir: ".", keys: []string{"./app", "app", "app/sub/.."}, resolved: "app"},
		{name: "root relative", configDir: "nested", keys: []string{"../app", "/app"}, resolved: "app"},
		{name: "nested config", configDir: "nested", keys: []string{"./app", "/nested/app", `app\sub\..`}, resolved: "nested/app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{SDKs: map[string]SDKEntry{"go": {Scopes: map[string]SDKScope{}}}}
			for _, key := range test.keys {
				cfg.SDKs["go"].Scopes[key] = SDKScope{Clients: []string{"./target", "./target"}}
			}
			messages, err := ReconcileSDKScopes(cfg, test.configDir)
			require.NoError(t, err)
			require.Len(t, messages, len(test.keys)-1)
			require.Len(t, cfg.SDKs["go"].Scopes, 1)
			key, found, err := SDKScopeKey(cfg.SDKs["go"], test.configDir, test.resolved)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, test.keys[0], key)
			require.Equal(t, []string{"./target"}, cfg.SDKs["go"].Scopes[key].Clients)
		})
	}

	t.Run("preserve clients and metadata", func(t *testing.T) {
		cfg := &Config{SDKs: map[string]SDKEntry{
			"go": {Scopes: map[string]SDKScope{
				"./app": {IsModule: true, Clients: []string{"./target"}, Settings: map[string]any{"shared": "same", "first": true}},
				"app":   {IsModule: true, Name: "checkout", Clients: []string{"./target", "././target"}, Settings: map[string]any{"shared": "same", "second": "value"}},
			}},
			"python": {Scopes: map[string]SDKScope{"app": {Name: "other"}}},
		}}
		_, err := ReconcileSDKScopes(cfg, ".")
		require.NoError(t, err)
		require.Equal(t, SDKScope{IsModule: true, Name: "checkout", Clients: []string{"./target", "././target"}, Settings: map[string]any{"shared": "same", "first": true, "second": "value"}}, cfg.SDKs["go"].Scopes["./app"])
		require.Equal(t, "other", cfg.SDKs["python"].Scopes["app"].Name)
	})

	for _, test := range []struct {
		field string
		first SDKScope
		last  SDKScope
	}{
		{field: "is-module", first: SDKScope{IsModule: true}},
		{field: "name", first: SDKScope{Name: "first"}, last: SDKScope{Name: "second"}},
		{field: "settings.runtime", first: SDKScope{Settings: map[string]any{"runtime": "node"}}, last: SDKScope{Settings: map[string]any{"runtime": "bun"}}},
	} {
		t.Run("conflicting "+test.field, func(t *testing.T) {
			cfg := &Config{SDKs: map[string]SDKEntry{"go": {Scopes: map[string]SDKScope{"./app": test.first, "/app": test.first, "app": test.last}}}}
			before := cloneConfig(cfg)
			_, err := ReconcileSDKScopes(cfg, ".")
			require.ErrorContains(t, err, `SDK "go" scope keys ["./app" "/app" "app"] resolve to "app"`)
			require.ErrorContains(t, err, test.field)
			require.Equal(t, before, cfg, "a conflict must not partially change the config")
		})
	}
}

func TestSDKScopeConfigWrite(t *testing.T) {
	data := []byte(`[modules.go-sdk]
source = "sdk"
[sdks.go]
module = "go-sdk"
[sdks.go.scopes."../app"]
clients = ["./target"]
[sdks.go.scopes."/app"]
name = "checkout"
`)
	cfg, err := ParseConfigAt(context.Background(), data, "nested")
	require.NoError(t, err)
	require.Len(t, cfg.SDKs["go"].Scopes, 1)
	written, err := UpdateConfigBytesAt(context.Background(), data, cfg, "nested")
	require.NoError(t, err)
	reparsed, err := ParseConfig(written)
	require.NoError(t, err)
	require.Equal(t, cfg, reparsed)
	require.Contains(t, string(written), `scopes."../app"`)
	require.NotContains(t, string(written), `scopes."/app"`)

	cfg.SDKs["go"].Scopes["/app"] = SDKScope{Name: "other"}
	_, err = UpdateConfigBytesAt(context.Background(), data, cfg, "nested")
	require.ErrorContains(t, err, "conflict in name")
}
