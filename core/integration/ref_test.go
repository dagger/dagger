package core

import (
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestRefIntegration(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	// This test handles a very edgy case:
	// goGitBase inits a /work directory with a git context
	// we then create a local dir with the same structure as a git remote: `/work/github.com/dagger/dagger`
	// It should be resolved as a local ref, not a remote one
	t.Run("local module with same format as remote: github.com/dagger/dagger", func(t *testing.T) {
		t.Parallel()
		out, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/github.com/dagger/dagger").
			WithExec([]string{"pwd"}).
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/github.com/dagger/dagger/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

				import "context"

				type Dep struct {}

				func (m *Dep) GetSource(ctx context.Context) string {
					return "hello"
				}
				`,
			}).
			WithWorkdir("/work").
			With(daggerCallAt("github.com/dagger/dagger", "get-source")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(out))
	})
}
