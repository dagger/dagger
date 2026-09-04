package daggercmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestPrintSDKList(t *testing.T) {
	cfg := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{
			"python-sdk": {Source: "github.com/dagger/python-sdk", Pin: "v1.2.3"},
			"go-sdk":     {Source: "./tools/go-sdk"},
		},
		SDKs: map[string]workspace.SDKEntry{
			"python": {Module: "python-sdk"},
			"go":     {Module: "go-sdk"},
		},
	}
	var out bytes.Buffer
	require.NoError(t, printSDKList(&out, cfg))
	require.Equal(t, []string{
		"SDK", "SOURCE",
		"go", "./tools/go-sdk",
		"python", "github.com/dagger/python-sdk@v1.2.3",
	}, strings.Fields(out.String()))
}

func TestSDKScopeRecordsAndFilters(t *testing.T) {
	cfg := &workspace.Config{SDKs: map[string]workspace.SDKEntry{
		"python": {
			Scopes: map[string]workspace.SDKScope{
				"web": {Name: "frontend"},
			},
		},
		"go": {
			Scopes: map[string]workspace.SDKScope{
				"api": {Name: "backend", IsModule: true},
			},
		},
	}}
	records, err := sdkScopeRecords(cfg, "apps")
	require.NoError(t, err)
	require.Equal(t, []string{"apps/api", "apps/web"}, []string{records[0].path, records[1].path})

	filtered := filterSDKScopeRecords(records, sdkScopeFilters{isModuleSet: true, isModule: false})
	require.Len(t, filtered, 1)
	require.Equal(t, "frontend", filtered[0].scope.Name)

	filtered = filterSDKScopeRecords(records, sdkScopeFilters{nameSet: true, name: "backend", sdkSet: true, sdk: "go"})
	require.Len(t, filtered, 1)
	require.Equal(t, "apps/api", filtered[0].path)
}

func TestSelectSDKScopeRecord(t *testing.T) {
	state := &sdkWorkspaceConfig{
		configDir: ".",
		cwd:       "apps/api/subdir",
		config: &workspace.Config{SDKs: map[string]workspace.SDKEntry{
			"go": {Scopes: map[string]workspace.SDKScope{
				".":        {Name: "root"},
				"apps":     {Name: "apps"},
				"apps/api": {Name: "api"},
			}},
		}},
	}
	record, err := selectSDKScopeRecord(state, "")
	require.NoError(t, err)
	require.Equal(t, "api", record.scope.Name)

	state.cwd = "."
	record, err = selectSDKScopeRecord(state, "apps")
	require.NoError(t, err)
	require.Equal(t, "apps", record.scope.Name)

	_, err = selectSDKScopeRecord(state, "missing")
	require.EqualError(t, err, `no SDK scope exists at path "missing"`)
}

func TestSelectSDKScopeRecordRejectsAmbiguousPath(t *testing.T) {
	state := &sdkWorkspaceConfig{
		configDir: ".",
		cwd:       "apps/api",
		config: &workspace.Config{SDKs: map[string]workspace.SDKEntry{
			"go":     {Scopes: map[string]workspace.SDKScope{"apps/api": {}}},
			"python": {Scopes: map[string]workspace.SDKScope{"apps/api": {}}},
		}},
	}
	_, err := selectSDKScopeRecord(state, "")
	require.EqualError(t, err, `path "apps/api" belongs to multiple SDK scopes`)
}

func TestUpdateSDKScopeField(t *testing.T) {
	newConfig := func() (*workspace.Config, sdkScopeRecord) {
		scope := workspace.SDKScope{
			IsModule: true,
			Name:     "api",
			Clients:  []string{"database"},
			Settings: map[string]any{"mode": "fast"},
		}
		cfg := &workspace.Config{SDKs: map[string]workspace.SDKEntry{
			"go": {
				Module: "go-sdk",
				Scopes: map[string]workspace.SDKScope{"apps/api": scope},
			},
			"python": {Module: "python-sdk"},
		}}
		return cfg, sdkScopeRecord{
			path:       "apps/api",
			sdk:        "go",
			configPath: "apps/api",
			scope:      scope,
		}
	}

	t.Run("name", func(t *testing.T) {
		cfg, record := newConfig()
		require.NoError(t, updateSDKScopeField(cfg, record, "name", []string{"service"}, false))
		require.Equal(t, "service", cfg.SDKs["go"].Scopes["apps/api"].Name)
		require.EqualError(t, updateSDKScopeField(cfg, record, "name", nil, true), "scope name is required when is-module is true")
		require.NoError(t, updateSDKScopeField(cfg, record, "is-module", []string{"false"}, false))
		require.NoError(t, updateSDKScopeField(cfg, record, "name", nil, true))
		require.Empty(t, cfg.SDKs["go"].Scopes["apps/api"].Name)
	})

	t.Run("is-module", func(t *testing.T) {
		cfg, record := newConfig()
		require.NoError(t, updateSDKScopeField(cfg, record, "is-module", []string{"false"}, false))
		require.False(t, cfg.SDKs["go"].Scopes["apps/api"].IsModule)
		err := updateSDKScopeField(cfg, record, "is-module", []string{"invalid"}, false)
		require.ErrorContains(t, err, `invalid BOOL "invalid"`)
	})

	t.Run("module requires name", func(t *testing.T) {
		cfg, record := newConfig()
		scope := cfg.SDKs["go"].Scopes["apps/api"]
		scope.IsModule = false
		scope.Name = ""
		cfg.SDKs["go"].Scopes["apps/api"] = scope
		record.scope = scope
		err := updateSDKScopeField(cfg, record, "is-module", []string{"true"}, false)
		require.EqualError(t, err, "scope name is required when is-module is true")
	})

	t.Run("sdk", func(t *testing.T) {
		cfg, record := newConfig()
		require.NoError(t, updateSDKScopeField(cfg, record, "sdk", []string{"python"}, false))
		require.NotContains(t, cfg.SDKs["go"].Scopes, "apps/api")
		moved := cfg.SDKs["python"].Scopes["apps/api"]
		require.Equal(t, []string{"database"}, moved.Clients)
		require.Equal(t, map[string]any{"mode": "fast"}, moved.Settings)
	})

	t.Run("same sdk", func(t *testing.T) {
		cfg, record := newConfig()
		require.NoError(t, updateSDKScopeField(cfg, record, "sdk", []string{"go"}, false))
		require.Contains(t, cfg.SDKs["go"].Scopes, "apps/api")
	})

	t.Run("unknown sdk", func(t *testing.T) {
		cfg, record := newConfig()
		err := updateSDKScopeField(cfg, record, "sdk", []string{"rust"}, false)
		require.EqualError(t, err, "SDK \"rust\" is not known; use `dagger sdk list`")
		require.Contains(t, cfg.SDKs["go"].Scopes, "apps/api")
	})

	t.Run("unset sdk", func(t *testing.T) {
		cfg, record := newConfig()
		require.NoError(t, updateSDKScopeField(cfg, record, "sdk", nil, true))
		require.Empty(t, cfg.SDKs["go"].Scopes)
	})
}
