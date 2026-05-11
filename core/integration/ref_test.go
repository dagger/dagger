package core

// These tests cover strings that identify module sources, such as local paths,
// remote refs, and Git-looking paths. They verify how those refs resolve before
// module loading begins.
//
// See also:
// - git_test.go: Git repository fetch behavior.
// - module_loading_test.go: source selection after a reference resolves.

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestRefIntegration(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// This test handles a very edgy case:
	// goGitBase inits a /work directory with a git context
	// we then create a local dir with the same structure as a git remote: `/work/github.com/dagger/dagger`
	// It should be resolved as a local ref, not a remote one
	t.Run("local module with same format as remote: github.com/dagger/dagger", func(ctx context.Context, t *testctx.T) {
		out, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/github.com/dagger/dagger").
			WithExec([]string{"pwd"}).
			With(daggerExec("module", "init", "--source=.", "--sdk=go", "dep", ".")).
			WithNewFile("/work/github.com/dagger/dagger/main.go", `package main

				import "context"

				type Dep struct {}

				func (m *Dep) GetSource(ctx context.Context) string {
					return "hello"
				}
				`,
			).
			WithWorkdir("/work").
			With(daggerCallAt("github.com/dagger/dagger", "get-source")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(out))
	})
}
