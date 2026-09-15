package core

// These tests cover old single-module `dagger.json` fields that are still
// accepted by module loading.
//
// See also:
// - module_config_test.go: current module config behavior.
// - workspace_compat_test.go: legacy workspace inference from `dagger.json`.

import (
	"context"
	"strings"

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
