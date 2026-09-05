package schema

import (
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func stagedConfigWithClientScopes(t *testing.T) *stagedWorkspaceConfig {
	t.Helper()
	return &stagedWorkspaceConfig{
		ConfigDir: "app",
		Config: &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"target": {Source: "target"},
			},
			SDKs: map[string]workspace.SDKEntry{
				"go": {
					Module: "go-sdk",
					Scopes: map[string]workspace.SDKScope{
						".": {
							IsModule: true,
							Name:     "app-dev",
							Clients:  []string{"target", "github.com/acme/api@main"},
						},
						"nested": {
							IsModule: true,
							Name:     "nested",
							Clients:  []string{"github.com/acme/other@main"},
						},
					},
				},
				"dang": {
					Module: "dang-sdk",
					Scopes: map[string]workspace.SDKScope{
						".": {IsModule: true, Name: "dang-dev", Clients: []string{"target"}},
					},
				},
			},
		},
	}
}

func TestSelectSDKModuleClients(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cwd     string
		args    sdkModuleClientUpdateArgs
		want    map[string][]string // "sdk@workspaceScope" -> targets
		wantErr string
	}{
		{
			name: "cwd selects only the scopes containing it",
			cwd:  "app",
			args: sdkModuleClientUpdateArgs{},
			want: map[string][]string{
				"dang@app": {"target"},
				"go@app":   {"target", "github.com/acme/api@main"},
			},
		},
		{
			name: "all selects every scope",
			cwd:  "app",
			args: sdkModuleClientUpdateArgs{All: true},
			want: map[string][]string{
				"dang@app":      {"target"},
				"go@app":        {"target", "github.com/acme/api@main"},
				"go@app/nested": {"github.com/acme/other@main"},
			},
		},
		{
			name: "sdk filters by provider",
			cwd:  "app",
			args: sdkModuleClientUpdateArgs{SDK: "go"},
			want: map[string][]string{"go@app": {"target", "github.com/acme/api@main"}},
		},
		{
			name: "modules filter selects named targets across sdks",
			cwd:  "app",
			args: sdkModuleClientUpdateArgs{Modules: []string{"target"}},
			want: map[string][]string{
				"dang@app": {"target"},
				"go@app":   {"target"},
			},
		},
		{
			name: "nested cwd selects the parent scope too",
			cwd:  "app/nested",
			args: sdkModuleClientUpdateArgs{},
			want: map[string][]string{
				"dang@app":      {"target"},
				"go@app":        {"target", "github.com/acme/api@main"},
				"go@app/nested": {"github.com/acme/other@main"},
			},
		},
		{
			name:    "unknown target is an error",
			cwd:     "app",
			args:    sdkModuleClientUpdateArgs{Modules: []string{"missing"}},
			wantErr: `client target "missing" is not recorded`,
		},
		{
			name:    "target outside the selected scope is an error",
			cwd:     "app",
			args:    sdkModuleClientUpdateArgs{Modules: []string{"github.com/acme/other@main"}},
			wantErr: `client target "github.com/acme/other@main" is not recorded`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			selections, err := selectSDKModuleClients(stagedConfigWithClientScopes(t), test.cwd, test.args)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			got := map[string][]string{}
			for _, selection := range selections {
				got[selection.sdkName+"@"+selection.workspaceScope] = selection.targets
			}
			require.Equal(t, test.want, got)
		})
	}
}

// A scope with no matching client must not produce a selection, so the caller
// does not regenerate a scope that nothing selected.
func TestSelectSDKModuleClientsSkipsEmptyScopes(t *testing.T) {
	t.Parallel()

	staged := stagedConfigWithClientScopes(t)
	staged.Config.SDKs["go"].Scopes["empty"] = workspace.SDKScope{IsModule: true, Name: "empty"}

	selections, err := selectSDKModuleClients(staged, "app", sdkModuleClientUpdateArgs{All: true})
	require.NoError(t, err)
	for _, selection := range selections {
		require.NotEmpty(t, selection.targets)
		require.NotEqual(t, "app/empty", selection.workspaceScope)
	}
}

func TestSelectSDKModuleClientsForModuleSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sources []string
		want    map[string][]string
	}{
		{
			name:    "remote module",
			sources: []string{"github.com/acme/api@main"},
			want: map[string][]string{
				"go@app": {"github.com/acme/api@main"},
			},
		},
		{
			name:    "local module",
			sources: []string{"app/target"},
			want: map[string][]string{
				"dang@app": {"target"},
				"go@app":   {"target"},
			},
		},
		{
			name:    "module without a client",
			sources: []string{"github.com/acme/unused@main"},
			want:    map[string][]string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			selections, err := selectSDKModuleClientsForModuleSources(stagedConfigWithClientScopes(t), test.sources)
			require.NoError(t, err)
			got := map[string][]string{}
			for _, selection := range selections {
				got[selection.sdkName+"@"+selection.workspaceScope] = selection.targets
			}
			require.Equal(t, test.want, got)
		})
	}
}

func TestOrderSDKModuleClientSelections(t *testing.T) {
	t.Parallel()

	staged := &stagedWorkspaceConfig{
		ConfigDir: ".",
		Config: &workspace.Config{SDKs: map[string]workspace.SDKEntry{
			"go": {
				Module: "go-sdk",
				Scopes: map[string]workspace.SDKScope{
					".": {
						IsModule: true,
						Name:     "root",
						Clients:  []string{"nested"},
					},
					"nested": {
						IsModule: true,
						Name:     "nested",
						Clients:  []string{"github.com/acme/api@main"},
					},
				},
			},
		}},
	}
	selections, err := selectSDKModuleClients(staged, ".", sdkModuleClientUpdateArgs{All: true})
	require.NoError(t, err)
	ordered, err := orderSDKModuleClientSelections(staged, selections)
	require.NoError(t, err)

	paths := make([]string, len(ordered))
	for i, selection := range ordered {
		paths[i] = selection.workspaceScope
	}
	require.Equal(t, []string{"nested", "."}, paths)
}
