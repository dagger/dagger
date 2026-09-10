package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectModule(t *testing.T) {
	modules := map[string]ModuleEntry{
		"tools":  {Source: "github.com/acme/repo/tools@v1"},
		"api":    {Source: "https://github.com/acme/repo.git#main:api"},
		"local":  {Source: "./modules/local"},
		"custom": {Source: "https://custom.example/a#main:b"},
	}
	for _, tc := range []struct {
		selector, name, source, version string
	}{
		{"tools", "tools", "", ""},
		{"tools@v2", "tools", "", "v2"},
		{"tools@feature/branch", "tools", "", "feature/branch"},
		{"tools@feature.v2/branch", "tools", "", "feature.v2/branch"},
		{"tools@feature@backup", "tools", "", "feature@backup"},
		{"github.com/acme/repo/tools@v2", "tools", "github.com/acme/repo/tools", "v2"},
		{"https://github.com/acme/repo/tools", "tools", "github.com/acme/repo/tools", ""},
		{"https://user:password@github.com/acme/repo/tools@v2", "tools", "github.com/acme/repo/tools", "v2"},
		{"GITHUB.COM/acme/repo/tools", "tools", "github.com/acme/repo/tools", ""},
		{"git@github.com:acme/repo.git/tools@v3", "tools", "github.com/acme/repo/tools", "v3"},
		{"git@github.com:acme/repo.git/tools@feature@backup", "tools", "github.com/acme/repo/tools", "feature@backup"},
		{"ssh://git@github.com/acme/repo.git#v4:tools", "tools", "github.com/acme/repo/tools", "v4"},
		{"github.com/acme/repo/api", "api", "github.com/acme/repo/api", ""},
		{"../modules/local", "local", "project/modules/local", ""},
		{"https://custom.example/a#next:b", "custom", "custom.example/a.git/b", "next"},
	} {
		t.Run(tc.selector, func(t *testing.T) {
			selection, err := SelectModule(modules, "project", "project/app", tc.selector, true)
			require.NoError(t, err)
			require.Equal(t, tc.name, selection.Name)
			require.Equal(t, tc.source, selection.Source)
			require.Equal(t, tc.version, selection.Version)
			require.Equal(t, modules[tc.name], selection.Entry)
		})
	}

	_, err := SelectModule(modules, ".", ".", "github.com/acme/repo/other", false)
	require.EqualError(t, err, `module "github.com/acme/repo/other" is not installed in the workspace`)
	_, err = SelectModule(modules, ".", ".", "tools@v1", false)
	require.ErrorContains(t, err, "version selector is not allowed")
	_, err = SelectModule(modules, ".", ".", "tools@", true)
	require.ErrorContains(t, err, "version must not be empty")

	modules["older"] = ModuleEntry{Source: "github.com/acme/repo/tools@v0"}
	_, err = SelectModule(modules, ".", ".", "github.com/acme/repo/tools@v1", true)
	require.EqualError(t, err, `source "github.com/acme/repo/tools" matches installed modules "older", "tools"; use an installed name`)
	modules["github.com/acme/repo/tools"] = ModuleEntry{Source: "./different"}
	selection, err := SelectModule(modules, ".", ".", "github.com/acme/repo/tools", false)
	require.NoError(t, err)
	require.Equal(t, "./different", selection.Entry.Source)
	require.Empty(t, selection.Source)
}

func TestModuleSourceVersions(t *testing.T) {
	for _, tc := range []struct{ before, after string }{
		{"github.com/acme/repo/tools@v1", "github.com/acme/repo/tools@v2"},
		{"github.com/acme/repo/tools", "github.com/acme/repo/tools@v2"},
		{"git@github.com:acme/repo.git@v1", "git@github.com:acme/repo.git@v2"},
		{"ssh://git@github.com/acme/repo.git#main:tools", "ssh://git@github.com/acme/repo.git#v2:tools"},
	} {
		after, err := ModuleSourceWithVersion(tc.before, "v2")
		require.NoError(t, err, tc.before)
		require.Equal(t, tc.after, after)
	}
	_, err := ModuleSourceWithVersion("./local", "v2")
	require.ErrorContains(t, err, "local module source")
	_, err = ModuleSourceWithVersion("github.com/acme/repo", "")
	require.ErrorContains(t, err, "invalid module version")
	require.True(t, SameModuleRequest("github.com/acme/repo@v1", ".", "https://github.com/acme/repo@v1", "."))
	require.False(t, SameModuleRequest("github.com/acme/repo@v1", ".", "https://github.com/acme/repo#v1", "."))
	require.False(t, SameModuleRequest("github.com/acme/repo@v1", ".", "github.com/acme/repo@v2", "."))
	require.NotEqual(t,
		ModuleSourceIdentity("https://custom.example/a.git#main:b", "."),
		ModuleSourceIdentity("https://custom.example/a/b.git#main", "."),
	)
	require.NotEqual(t,
		ModuleSourceIdentity("github.com/acme/repo/other.git@v1", "."),
		ModuleSourceIdentity("github.com/acme/repo/other@v1", "."),
	)
	require.Equal(t, "custom.example/a.git/b", ModuleSourceIdentity("https://custom.example/a.git#main:b", ".").String())
	require.Equal(t,
		ModuleSourceIdentity("https://custom.example/a.git#main:b", "."),
		ModuleSourceIdentity("https://custom.example/a.git/b@v1", "."),
	)
}

func TestSelectModuleUpdates(t *testing.T) {
	modules := map[string]ModuleEntry{
		"tools": {Source: "github.com/acme/repo/tools@v1"},
		"local": {Source: "./local"},
	}
	for _, tc := range []struct {
		selectors []string
		version   string
		err       string
	}{
		{nil, "v2", "--version requires exactly one"},
		{[]string{"tools", "local"}, "v2", "--version requires exactly one"},
		{[]string{"tools@v2"}, "v2", "use either a version suffix or --version"},
		{[]string{"local"}, "v2", "local module source"},
		{[]string{"tools@v1", "github.com/acme/repo/tools@v2"}, "", "conflicting update requests"},
	} {
		_, err := SelectModuleUpdates(modules, ".", ".", tc.selectors, tc.version)
		require.ErrorContains(t, err, tc.err)
	}
	selections, err := SelectModuleUpdates(modules, ".", ".", []string{"github.com/acme/repo/tools"}, "v2")
	require.NoError(t, err)
	require.Equal(t, "tools", selections[0].Name)
	require.Equal(t, "v2", selections[0].Version)
	require.Equal(t, "github.com/acme/repo/tools", selections[0].Source)
	selections, err = SelectModuleUpdates(modules, ".", ".", nil, "")
	require.NoError(t, err)
	require.Equal(t, []string{"local", "tools"}, []string{selections[0].Name, selections[1].Name})
	selections, err = SelectModuleUpdates(modules, ".", ".", []string{"tools@v2", "github.com/acme/repo/tools@v2"}, "")
	require.NoError(t, err)
	require.Len(t, selections, 1)
}
