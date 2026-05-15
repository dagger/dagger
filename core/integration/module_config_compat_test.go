package core

// These tests cover old single-module `dagger.json` fields that are still
// accepted. They verify `dagger develop` normalizes include/exclude,
// dependencies, source, SDK, engine version, and dependency pins.
//
// See also:
// - module_config_test.go: current module config behavior.
// - workspace_compat_test.go: legacy workspace inference from `dagger.json`.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleConfigSuite) TestOldModuleConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		WithNewFile("dagger.json", `{
  "name": "oldmod",
  "sdk": "dang"
}`).
		WithNewFile("main.dang", `
type Oldmod {
  pub message: String! {
    "old module config loaded"
  }
}
`).
		With(daggerCallAt(".", "message")).
		CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "old module config loaded")
}

func (ModuleConfigSuite) TestOldModuleConfigPinnedDeps(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	repo := "github.com/dagger/dagger-test-modules/versioned"
	branch := "main"
	commit := "82adc5f7997e43ab3027810347298405f32a44db"

	out, err := goGitBase(t, c).
		WithNewFile("dagger.json", `{
  "name": "oldmod",
  "sdk": "dang",
  "dependencies": [
    {
      "name": "versioned",
      "source": "`+repo+`@`+branch+`",
      "pin": "`+commit+`"
    }
  ]
}`).
		WithNewFile("main.dang", `
type Oldmod {
  pub dependencyHello: String! {
    versioned.hello
  }
}
`).
		With(daggerCallAt(".", "dependency-hello")).
		CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, strings.ToLower(out), "version 2")
}

func (ModuleConfigSuite) TestLegacyModuleConfigUpgrade(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseWithOldConfig := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/foo").
		With(daggerExec("module", "init", "--source=.", "--sdk=go", "dep", ".")).
		WithWorkdir("/work").
		With(daggerExec("module", "init", "--source=.", "--sdk=go", "test", ".")).
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
		With(daggerExec("module", "init", "--source=.", "--sdk=go", "test", "."))
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
