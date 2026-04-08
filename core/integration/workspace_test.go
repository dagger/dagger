package core

import (
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
)

type WorkspaceSuite struct{}

func TestWorkspace(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSuite{})
}

// workspaceBase returns a container with git, the dagger CLI, and an
// initialized git repo at /work — the starting point for workspace tests.
func workspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

func legacyWorkspaceBase(t testing.TB, c *dagger.Client, config string, ops ...dagger.WithContainerFunc) *dagger.Container {
	t.Helper()

	ctr := workspaceBase(t, c).
		WithNewFile("dagger.json", config)
	for _, op := range ops {
		ctr = ctr.With(op)
	}

	return ctr.
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})
}

func ensureWorkspaceInit() dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithExec([]string{"sh", "-c", "test -f .dagger/config.toml || dagger workspace init"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

// initDangModule creates a config-owned Dang workspace module with the given
// name and source code.
func initDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(ensureWorkspaceInit()).
			With(daggerExec("module", "init", "--sdk=dang", "--name="+name)).
			WithNewFile(".dagger/modules/"+name+"/main.dang", source)
	}
}

// initStandaloneDangModule creates a standalone Dang module in the current
// working directory and overwrites main.dang with the provided source.
func initStandaloneDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerExec("module", "init", "--sdk=dang", "--source=.", "--name="+name)).
			WithNewFile("main.dang", source)
	}
}

// initDangBlueprint creates a config-owned Dang workspace module and marks it
// as the workspace entrypoint so its methods are promoted to the root.
func initDangBlueprint(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(ensureWorkspaceInit()).
			With(daggerExec("module", "init", "--sdk=dang", "--name="+name)).
			WithNewFile(".dagger/modules/"+name+"/main.dang", source).
			With(daggerExec("workspace", "config", "modules."+name+".entrypoint", "true"))
	}
}
