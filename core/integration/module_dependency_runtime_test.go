package core

// These tests cover a module calling other installed modules from its own
// functions. They assume dependency entries already exist in config, then verify
// runtime calls and schema exposure.
//
// See also:
// - workspace_modules_test.go: workspace-level module installation/configuration.

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestConflictingSameNameTransitiveDeps covers two distinct dependency-graph
// contracts for A -> B -> Dint and A -> C -> Dstr, where both D modules have
// the same name and object names but incompatible field types.
func (ModuleSuite) TestConflictingSameNameTransitiveDeps(ctx context.Context, t *testctx.T) {
	// This setup is often slow locally; keep the two contracts below in one test
	// so they share the same dependency graph.
	if testing.Short() {
		t.SkipNow()
	}

	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		With(withTestdataFixture(t, c, ".", "modules/go/conflicting-transitive-deps")).
		WithWorkdir("/work/a")

	t.Run("runtime resolves conflicting transitive deps", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerQueryAt(".", `{fn}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"fn": "foo123"}`, out)
	})

	t.Run("schema exposes only direct deps", func(ctx context.Context, t *testctx.T) {
		types := currentSchema(ctx, t, ctr).Types
		require.NotNil(t, types.Get("A"))
		require.NotNil(t, types.Get("B"))
		require.NotNil(t, types.Get("C"))
		require.Nil(t, types.Get("D"))
	})
}

type localDepTestCase struct {
	sdk     string
	fixture string
}

var useLocalDepTestCases = []localDepTestCase{
	{
		sdk:     "go",
		fixture: "go/local-dep-parent",
	},
	{
		sdk:     "python",
		fixture: "python/local-dep-parent",
	},
	{
		sdk:     "typescript",
		fixture: "typescript/local-dep-parent",
	},
}

// TestUseLocalDependencyFromParentModule verifies the core local-dependency
// contract: a parent module installs a dependency by relative path, then client
// calls into the parent module can execute parent code that calls the dependency.
func (ModuleSuite) TestUseLocalDependencyFromParentModule(ctx context.Context, t *testctx.T) {
	for _, tc := range useLocalDepTestCases {
		t.Run(fmt.Sprintf("%s parent calls local dependency", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := testModuleWithLocalDep(t, c, tc.fixture)

			out, err := modGen.With(daggerQueryAt(".", `{useHello}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"useHello":"hello"}`, out)
		})
	}
}

// TestUseLocalDependencySchemaIsolation verifies that loading a parent module
// does not promote local dependency root fields onto the client Query schema.
// Parent-module use of the dependency is covered above.
func (ModuleSuite) TestUseLocalDependencySchemaIsolation(ctx context.Context, t *testctx.T) {
	for _, tc := range useLocalDepTestCases {
		t.Run(fmt.Sprintf("%s schema hides local dependency", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := testModuleWithLocalDep(t, c, tc.fixture)

			_, err := modGen.With(daggerQueryAt(".", `{dep{hello}}`)).Stdout(ctx)
			requireErrOut(t, err, `Cannot query field \"dep\" on type \"Query\"`)
		})
	}
}

func testModuleWithLocalDep(t *testctx.T, c *dagger.Client, fixture string) *core.Container {
	return moduleFixture(t, c, fixture)
}

// TestRuntimeDependencyDoesNotInheritWorkspace covers the A -> B runtime
// boundary: A receives the caller's contextual workspace, but dependency module
// B only receives it when A passes it explicitly.
func (ModuleSuite) TestRuntimeDependencyDoesNotInheritWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := moduleFixture(t, c, "go/runtime-workspace-isolation").
		WithNewFile("marker.txt", "workspace marker")

	// A can still share its workspace with B when it passes the value explicitly.
	//
	// Driven through `dagger call` rather than a raw query: A's own Workspace!
	// is required, and the CLI defaults it to the current workspace. A raw
	// GraphQL document gets no such help — it fails validation before reaching
	// the engine — so passing one there means naming the ID by hand.
	t.Run("explicit workspace pass succeeds", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "explicit-workspace-arg")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "workspace marker", strings.TrimSpace(out))
	})

	// B must not receive A's contextual workspace through an omitted argument.
	t.Run("workspace argument is not auto-injected into dependency", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerQueryAt(".", `{implicitWorkspaceArg}`)).Stdout(ctx)
		requireErrOut(t, err, "workspace arguments are not inherited by module runtime calls; pass a Workspace explicitly")
	})

	// The currentWorkspace vector is closed at the schema level: currentWorkspace
	// is not exposed to module SDKs (covered by TestCurrentWorkspaceError), so a
	// dependency cannot reach it to inherit one.
}

// sameContentModule is a Dang module whose constructor takes a Workspace and
// keeps only a read of greeting.txt from it, so every object it makes in one
// workspace holds the same content, whatever its type is named.
func sameContentModule(typeName string) string {
	return fmt.Sprintf(`type %s {
  pub source: Directory!

  new(ws: Workspace!) {
    self.source = ws.directory("/", include: ["greeting.txt"])
    self
  }

  pub greeting: String! {
    source.file("greeting.txt").contents
  }

  pub greet(extra: Directory!): String! {
    source.file("greeting.txt").contents
  }
}
`, typeName)
}

// TestSameContentObjectsKeepTheirTypes covers objects of two types that hold
// the same content, made in one session. Each is still an object of its own
// type: a call on one never gets the other. dagger/dagger's own CI makes such
// a pair: its client modules load engine-dev under the name dagger-engine, so
// its object is a DaggerEngine there and an EngineDev elsewhere.
//
// The calling module passes a directory it builds in the same expression, as
// the client modules pass a container to the engine's installClient. Dang
// then makes the object first, gets its ID, and calls the function on the
// object it loads from that ID.
func (ModuleSuite) TestSameContentObjectsKeepTheirTypes(ctx context.Context, t *testctx.T) {
	// Each case reads a greeting no other test has, so only its own objects
	// hold the same content.
	base := func(t *testctx.T, c *dagger.Client, greeting, appDeps, appFunction string) *core.Container {
		return goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.builder]
source = ".dagger/modules/builder"

[modules.app]
source = ".dagger/modules/app"
`).
			WithNewFile("greeting.txt", greeting).
			WithNewFile(".dagger/modules/builder/dagger-module.toml", `name = "builder"

[runtime]
source = "dang"
`).
			WithNewFile(".dagger/modules/builder/main.dang", sameContentModule("Builder")).
			WithNewFile(".dagger/modules/twin/dagger-module.toml", `name = "twin"

[runtime]
source = "dang"
`).
			WithNewFile(".dagger/modules/twin/main.dang", sameContentModule("Twin")).
			WithNewFile(".dagger/modules/app/dagger-module.toml", `name = "app"

[runtime]
source = "dang"
`+appDeps).
			WithNewFile(".dagger/modules/app/main.dang", `type App {
  let ws: Workspace!

`+appFunction+`
}
`)
	}

	// The same module, called under its own name and then as a dependency
	// under another, as engine-dev is.
	t.Run("one module under two names", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		greeting := rand.Text() + "\n"
		out, err := base(t, c, greeting, `
[[dependencies]]
name = "renamed"
source = "../builder"
`, `  pub viaRenamed: String! {
    renamed(ws: self.ws).greet(extra: directory.withNewFile("extra.txt", "extra"))
  }`).
			With(daggerShell("builder | greeting\napp | via-renamed\n")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, greeting+greeting, out)
	})

	// Two modules whose objects hold the same content, called one after the
	// other in one function.
	t.Run("two modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		greeting := rand.Text() + "\n"
		out, err := base(t, c, greeting, `
[[dependencies]]
name = "builder"
source = "../builder"

[[dependencies]]
name = "twin"
source = "../twin"
`, `  pub both: String! {
    builder(ws: self.ws).greet(extra: directory.withNewFile("extra.txt", "extra")) +
      twin(ws: self.ws).greet(extra: directory.withNewFile("extra.txt", "extra"))
  }`).
			With(daggerShell("app | both\n")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, greeting+greeting, out)
	})
}

// TestSameNamedObjectsRunTheirOwnModule covers objects of one type name from
// two different modules that hold the same content, made in one session: two
// modules named d, each a dependency of another module. The cache can take the
// two objects as one result, so a call may be handed the other module's
// object. It must still run its own module's function.
func (ModuleSuite) TestSameNamedObjectsRunTheirOwnModule(ctx context.Context, t *testctx.T) {
	d := func(marker string) string {
		return `type D {
  pub source: Directory!

  new(ws: Workspace!) {
    self.source = ws.directory("/", include: ["greeting.txt"])
    self
  }

  pub greet(extra: Directory!): String! {
    "` + marker + `:" + source.file("greeting.txt").contents
  }
}
`
	}
	caller := func(name, dep string) (string, string) {
		return `name = "` + name + `"

[runtime]
source = "dang"

[[dependencies]]
name = "d"
source = "../` + dep + `"
`, `type ` + strings.ToUpper(name) + ` {
  pub greet(ws: Workspace!): String! {
    d(ws: ws).greet(extra: directory.withNewFile("extra.txt", "extra"))
  }
}
`
	}
	bConfig, bSource := caller("b", "d1")
	cConfig, cSource := caller("c", "d2")

	// A greeting no other test has, so only the two D objects hold the same
	// content.
	greeting := rand.Text() + "\n"
	c := connect(ctx, t)
	out, err := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.app]
source = ".dagger/modules/app"
`).
		WithNewFile("greeting.txt", greeting).
		WithNewFile(".dagger/modules/d1/dagger-module.toml", "name = \"d\"\n\n[runtime]\nsource = \"dang\"\n").
		WithNewFile(".dagger/modules/d1/main.dang", d("one")).
		WithNewFile(".dagger/modules/d2/dagger-module.toml", "name = \"d\"\n\n[runtime]\nsource = \"dang\"\n").
		WithNewFile(".dagger/modules/d2/main.dang", d("two")).
		WithNewFile(".dagger/modules/b/dagger-module.toml", bConfig).
		WithNewFile(".dagger/modules/b/main.dang", bSource).
		WithNewFile(".dagger/modules/c/dagger-module.toml", cConfig).
		WithNewFile(".dagger/modules/c/main.dang", cSource).
		WithNewFile(".dagger/modules/app/dagger-module.toml", `name = "app"

[runtime]
source = "dang"

[[dependencies]]
name = "b"
source = "../b"

[[dependencies]]
name = "c"
source = "../c"
`).
		WithNewFile(".dagger/modules/app/main.dang", `type App {
  let ws: Workspace!

  pub both: String! {
    b.greet(ws: self.ws) + c.greet(ws: self.ws)
  }
}
`).
		With(daggerShell("app | both\n")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "one:"+greeting+"two:"+greeting, out)
}

func (ModuleSuite) TestUseLocalMulti(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk     string
		fixture string
	}

	for _, tc := range []testCase{
		{
			sdk:     "go",
			fixture: "go/multi-dep-parent",
		},
		{
			sdk:     "python",
			fixture: "python/multi-dep-parent",
		},
		{
			sdk:     "typescript",
			fixture: "typescript/multi-dep-parent",
		},
	} {
		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := moduleFixture(t, c, tc.fixture).
				WithEnvVariable("BUST", rand.Text()) // NB(vito): hmm...

			out, err := modGen.With(daggerQueryAt(".", `{names}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"names":["foo", "bar"]}`, out)
		})
	}
}
