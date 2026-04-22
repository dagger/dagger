package core

// Workspace alignment: aligned; intentionally compat-focused.
// Scope: Legacy module-shaped dagger.json forms that are still accepted and normalized as module config.
// Intent: Keep supported module-config compatibility explicit without mixing it with workspace compat inference.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// This file owns old module-shaped dagger.json forms that are still accepted
// and normalized as module config. Legacy workspace inference from dagger.json
// does not belong here; it belongs in workspace_compat_test.go.
func (ModuleConfigSuite) TestLegacyModuleConfigUpgrade(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseWithOldConfig := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/foo").
		With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", `package main
		type Test struct {}

		func (m *Test) Fn() string { return "wowzas" }
		`,
		).
		WithNewFile("/work/dagger.json", `{"name": "test", "sdk": "go", "include": ["foo"], "exclude": ["blah", "!bar"], "dependencies": ["foo"]}`)

	// verify develop updates config to new format
	baseWithNewConfig := baseWithOldConfig.With(daggerExec("develop"))
	confContents, err := baseWithNewConfig.File("dagger.json").Contents(ctx)
	require.NoError(t, err)
	var modCfg modules.ModuleConfigWithUserFields
	require.NoError(t, json.Unmarshal([]byte(confContents), &modCfg))
	require.Equal(t, "test", modCfg.Name)
	require.Equal(t, &modules.SDK{Source: "go"}, modCfg.SDK)
	require.Equal(t, []string{"foo", "!blah", "bar"}, modCfg.Include)
	require.Empty(t, modCfg.Exclude)
	require.Len(t, modCfg.Dependencies, 1)
	require.Equal(t, "foo", modCfg.Dependencies[0].Source)
	require.Equal(t, "dep", modCfg.Dependencies[0].Name)
	require.Equal(t, ".", modCfg.Source)
	require.NotEmpty(t, modCfg.EngineVersion) // version changes with any engine change
	require.Empty(t, modCfg.Schema)

	// verify develop didn't overwrite main.go
	out, err := baseWithNewConfig.With(daggerCall("fn")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "wowzas", strings.TrimSpace(out))

	// verify call works seamlessly even without explicit sync yet
	out, err = baseWithOldConfig.With(daggerCall("fn")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "wowzas", strings.TrimSpace(out))
}

func (ModuleConfigSuite) TestLegacyModuleConfigPinsAreNormalized(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// get the latest commit on main
	repo := "github.com/dagger/dagger-test-modules"
	branch := "main"
	commit, err := c.Git(repo).Branch(branch).Commit(ctx)
	require.NoError(t, err)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go"))
	modCfgContents, err := ctr.
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)

	var modCfg modules.ModuleConfig
	require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
	modCfg.Dependencies = append(modCfg.Dependencies, &modules.ModuleConfigDependency{
		Name:   "root-mod",
		Source: repo + "@" + commit,
	})
	rewrittenModCfg, err := json.Marshal(modCfg)
	require.NoError(t, err)

	ctr = ctr.
		WithNewFile("dagger.json", string(rewrittenModCfg)).
		With(daggerExec("develop"))

	modCfgContents, err = ctr.
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)

	modCfg = modules.ModuleConfig{}
	require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
	require.Len(t, modCfg.Dependencies, 1)
	dep := modCfg.Dependencies[0]

	require.Equal(t, "root-mod", dep.Name)
	require.Equal(t, repo+"@"+commit, dep.Source)
	require.Equal(t, commit, dep.Pin)
}
