package core

// These tests cover modules written in Dang that install other local modules as
// dependencies. They verify a Dang module calling Go, Python, TypeScript, and
// Dang child modules.
//
// See also:
// - module_dependency_runtime_test.go: runtime use of installed module dependencies.

import (
	"context"

	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

func (ModuleSuite) TestBuiltinDangDependencyModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleEntrypointFixture(t, c, "test", "dang/dependency-modules")

	for _, tc := range []struct {
		name string
		call string
		want string
	}{
		{name: "local", call: "local", want: "local"},
		{name: "dang child", call: "via-dang", want: "dang"},
		{name: "python child", call: "via-python", want: "python"},
		{name: "go child", call: "via-go", want: "go"},
		{name: "typescript child", call: "via-typescript", want: "typescript"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall(tc.call)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
}
