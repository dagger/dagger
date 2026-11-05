package core

// These tests cover how a Git workspace's modules load: sharing the loads of
// what they have in common. Each module here records its
// loads from its entrypoint's sources member, which runs each time its module
// source loads, into a cache volume that a reader module reports.

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// moduleLoadRecorder is the entrypoint of a module named %[1]s whose sources
// member runs script with a cache volume named %[2]s at /record. Random.string
// makes each load run it again rather than reuse an earlier run.
const moduleLoadRecorder = `type Entrypoint implements ModuleEntrypoint {
  pub sources(workspace: Workspace!): Directory! {
    directory
      .withDirectory(workspace.cwd, workspace.directory(".", gitignore: true))
      .withNewFile(".record", container
        .from("` + alpineImage + `")
        .withMountedCache("/record", cacheVolume("%[2]s"))
        .withEnvVariable("LOAD", Random.string)
        .withExec(["sh", "-c", %[3]q])
        .stdout)
  }

  pub types: [TypeDef!]! {
    [typeDef.withObject("%[1]s").withConstructor(function("", typeDef.withObject("%[1]s")))]
  }

  pub call(receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!): JSON! {
    ("{}" :: JSON!)
  }
}
`

// withModuleLoadRecorder adds a module at .dagger/modules/<name> to repo whose
// sources member runs script on each load.
func withModuleLoadRecorder(repo *core.Directory, name, volume, script string) *core.Directory {
	return repo.
		WithNewFile(".dagger/modules/"+name+"/dagger-module.toml", `name = "`+name+`"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithNewFile(".dagger/modules/"+name+"/entrypoint/main.dang",
			fmt.Sprintf(moduleLoadRecorder, strings.ToUpper(name[:1])+name[1:], volume, script))
}

// listGitWorkspaceChecks lists the checks of repo served as a Git workspace,
// from an empty directory, as a user would; this loads all its modules.
func listGitWorkspaceChecks(ctx context.Context, t *testctx.T, c *dagger.Client, repo *core.Directory) {
	t.Helper()
	remote := workspaceSelectionRemoteRef(ctx, t, c, repo)
	_, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty").
		WithExec([]string{"dagger", "--progress=report", "-W", remote, "check", "-l"}, core.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).
		Sync(ctx)
	require.NoError(t, err)
}

// readModuleLoadRecord returns the output of script run with the cache volume
// named volume at /record. Cache volumes an entrypoint makes share one
// namespace, which a test client can't mount, so another entrypoint module
// runs it.
func readModuleLoadRecord(ctx context.Context, t *testctx.T, c *dagger.Client, volume, script string) string {
	t.Helper()
	reader := core.NewQuery(c).Directory().
		WithNewFile("dagger-module.toml", `name = "reader"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithNewFile("entrypoint/main.dang", `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace): [TypeDef!]! {
    [
      typeDef
        .withObject("Reader")
        .withConstructor(function("", typeDef.withObject("Reader")))
        .withFunction(function("Record", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(
    workspace: Workspace,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      (JSON.encode(container
        .from("`+alpineImage+`")
        .withMountedCache("/record", cacheVolume("`+volume+`"))
        .withEnvVariable("READ", Random.string)
        .withExec(["sh", "-c", `+fmt.Sprintf("%q", script)+`])
        .stdout
        .trimSpace) :: JSON!)
    }
  }
}
`)
	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty").
		WithDirectory("/reader", reader).
		With(workspaceSelectionDaggerCall("-m", "/reader", "record")).
		Stdout(ctx)
	require.NoError(t, err)
	return strings.TrimSpace(out)
}

// TestGitWorkspaceSharedDependencyLoadsOnce asserts that modules of a Git
// workspace that share a dependency load it once in a session, though their
// loads run at the same time.
func (WorkspaceSuite) TestGitWorkspaceSharedDependencyLoadsOnce(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Unique per run, so earlier runs' loads can't satisfy the test.
	volume := "shared-dependency-loads-" + rand.Text()
	const users = 6

	config := ""
	repo := withModuleLoadRecorder(core.NewQuery(c).Directory(), "dep", volume, "echo load >> /record/loads")
	for i := range users {
		name := fmt.Sprintf("user%d", i)
		config += fmt.Sprintf("[modules.%s]\nsource = \".dagger/modules/%s\"\n\n", name, name)
		repo = repo.
			WithNewFile(".dagger/modules/"+name+"/dagger-module.toml", `name = "`+name+`"

[[dependencies]]
name = "dep"
source = "../dep"

[runtime]
source = "dang"
`).
			WithNewFile(".dagger/modules/"+name+"/main.dang", "type "+strings.ToUpper(name[:1])+name[1:]+" {\n  pub name: String! {\n    \""+name+"\"\n  }\n}\n")
	}
	listGitWorkspaceChecks(ctx, t, c, repo.WithNewFile("dagger.toml", config))

	loads := readModuleLoadRecord(ctx, t, c, volume, "cat /record/loads 2>/dev/null | wc -l")
	require.Equal(t, "1", loads, "the modules' shared dependency must load once")
}

// TestGitWorkspaceDependencyCycleFails asserts that two modules of a Git
// workspace that depend on each other fail to load with the cycle named,
// though both load at once, each reaching the other's load as its dependency.
func (WorkspaceSuite) TestGitWorkspaceDependencyCycleFails(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	repo := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", `[modules.ping]
source = ".dagger/modules/ping"

[modules.pong]
source = ".dagger/modules/pong"
`)
	for name, dep := range map[string]string{"ping": "pong", "pong": "ping"} {
		repo = repo.
			WithNewFile(".dagger/modules/"+name+"/dagger-module.toml", `name = "`+name+`"

[[dependencies]]
name = "`+dep+`"
source = "../`+dep+`"

[runtime]
source = "dang"
`).
			WithNewFile(".dagger/modules/"+name+"/main.dang", "type "+strings.ToUpper(name[:1])+name[1:]+" {\n  pub name: String! {\n    \""+name+"\"\n  }\n}\n")
	}
	remote := workspaceSelectionRemoteRef(ctx, t, c, repo)

	// A cycle that isn't detected waits forever, so bound the wait.
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := core.NewQuery(c).Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty").
		WithExec([]string{"dagger", "--progress=report", "-W", remote, "check", "-l"}, core.ContainerWithExecOpts{
			UseEntrypoint: true,
			Expect:        core.ReturnTypeAny,
		}).
		CombinedOutput(listCtx)
	require.NoError(t, err)
	require.Contains(t, out, "has a circular dependency on itself")
}
