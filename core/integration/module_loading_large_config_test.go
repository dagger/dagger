package core

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleLoadingSuite) TestConfigLargerThanSingleFileRead(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	manifest := "# " + strings.Repeat("x", 4<<20) + `
name = "large"
engineVersion = "latest"

[runtime]
source = "dang"
`
	base := goGitBase(t, c).
		WithNewFile("dagger-module.toml", manifest).
		WithNewFile("main.dang", `type Large { pub message: String! { "loaded" } }`)
	out, err := base.With(moduleLoadingDaggerQuery(`{moduleSource(refString: "."){asModule{name}}}`, "-m", "core")).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"moduleSource":{"asModule":{"name":"large"}}}`, out)
}
