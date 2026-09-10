package core

import (
	"context"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (GeneratorsSuite) TestSDKModuleInitAppliesSettings(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	sdkModulePath, err := filepath.Abs("testdata/sdks/module-max-lifecycle")
	require.NoError(t, err)

	base := goGitBase(t, c).
		WithDirectory("/work/.dagger/modules/module-max-lifecycle", c.Host().Directory(sdkModulePath)).
		WithNewFile("/work/dagger.toml", `[modules.go-sdk]
source = ".dagger/modules/module-max-lifecycle"

[sdks.go]
module = "go-sdk"
`).
		WithWorkdir("/work").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c))

	initialized := base.With(daggerNonNestedExec(
		"module", "init", "go",
		"--name", "demo",
		"--path", "demo",
		"--starter", "empty",
		"-y",
	))
	out, err := initialized.CombinedOutput(ctx)
	require.NoError(t, err, out)

	main, err := initialized.File("/work/demo/main.go").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, main, "func (m *Module) Hello()")

	config, err := initialized.File("/work/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, config, `starter = "empty"`)
}
