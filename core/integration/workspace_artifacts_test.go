package core

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type WorkspaceArtifactsSuite struct{}

func TestWorkspaceArtifacts(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceArtifactsSuite{})
}

// goTestWorkspace builds a workspace with a Go-SDK module exposing nested
// collections: GoModules (keyed by path) -> GoModule -> GoTests (keyed by
// test name), the canonical "select and run go tests" shape.
func goTestWorkspace(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).
		WithNewFile("dagger.toml", `[modules.go]
source = "go"
entrypoint = true
`).
		WithNewFile("go/dagger.json", `{"name": "go", "sdk": "go", "source": "."}`).
		WithNewFile("go/main.go", `package main

import "strings"

type Go struct{}

// The Go modules in this workspace, as a collection keyed by path
func (m *Go) Modules() *GoModules {
	return &GoModules{Keys: []string{"./app", "./lib"}}
}

// A collection of Go modules, keyed by workspace path
// +collection
type GoModules struct {
	Keys []string
}

// Select one Go module by path
func (c *GoModules) Get(path string) *GoModule {
	return &GoModule{Path: path}
}

// A Go module in the workspace
type GoModule struct {
	Path string
}

// The tests of this Go module, as a collection keyed by test name
func (m *GoModule) Tests() *GoTests {
	keys := []string{"TestLibUpper", "TestLibTrim"}
	if m.Path == "./app" {
		keys = []string{"TestAppGreet", "TestAppAdd"}
	}
	return &GoTests{Path: m.Path, Keys: keys}
}

// The tests of a Go module, as a collection keyed by test name
// +collection
type GoTests struct {
	Path string
	Keys []string
}

// Select a single test by name
func (t *GoTests) Get(name string) *GoTest {
	return &GoTest{Path: t.Path, Name: name}
}

// Run all tests in the current subset in a single invocation
func (t *GoTests) Run() string {
	return t.Path + ": go test -run '^(" + strings.Join(t.Keys, "|") + ")$'"
}

// A single Go test
type GoTest struct {
	Path string
	Name string
}

// Run this test
func (t *GoTest) Run() string {
	return t.Path + ": go test -run '^" + t.Name + "$'"
}
`)
}

// artifactsExecFail runs a dagger command that is expected to fail, capturing
// its error output in the container's combined output.
func artifactsExecFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func artifactsCallFail(args ...string) dagger.WithContainerFunc {
	return artifactsExecFail(append([]string{"call"}, args...)...)
}

func (WorkspaceArtifactsSuite) TestList(_ context.Context, t *testctx.T) {
	t.Run("dimensions", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerExecRaw("list")).
			Stdout(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{"types", "go-module", "go-test"},
			strings.Fields(out))
	})

	t.Run("types", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerExecRaw("list", "types")).
			Stdout(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{"go", "go-module", "go-test"},
			strings.Fields(out))
	})

	t.Run("collection dimension values", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerExecRaw("list", "go-module")).
			Stdout(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"./app", "./lib"}, strings.Fields(out))
	})

	t.Run("nested collection dimension values", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerExecRaw("list", "go-test")).
			Stdout(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{"TestAppGreet", "TestAppAdd", "TestLibUpper", "TestLibTrim"},
			strings.Fields(out))
	})

	t.Run("filter by parent collection dimension", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerExecRaw("list", "go-test", "--go-module=./app")).
			Stdout(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t,
			[]string{"TestAppGreet", "TestAppAdd"},
			strings.Fields(out))
	})

	t.Run("filter by coordinate value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerExecRaw("list", "go-test", "--go-test=TestLibTrim")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"TestLibTrim"}, strings.Fields(out))
	})

	t.Run("unknown dimension errors", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(artifactsExecFail("list", "bogus")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "unknown artifact dimension")
	})
}

func (WorkspaceArtifactsSuite) TestCollectionAlgebra(_ context.Context, t *testctx.T) {
	t.Run("keys", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerCall("modules", "keys")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"./app", "./lib"}, strings.Fields(out))
	})

	t.Run("get traverses to nested collection", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerCall("modules", "get", "--key", "./app", "tests", "keys")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"TestAppGreet", "TestAppAdd"}, strings.Fields(out))
	})

	t.Run("get unknown key errors", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(artifactsCallFail("modules", "get", "--key", "./nope", "path")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "does not contain key")
	})

	t.Run("subset narrows keys", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerCall("modules", "get", "--key", "./app", "tests",
				"subset", "--keys", "TestAppAdd", "keys")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"TestAppAdd"}, strings.Fields(out))
	})

	t.Run("subset rejects unknown keys", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(artifactsCallFail("modules", "get", "--key", "./app", "tests",
				"subset", "--keys", "TestNope", "keys")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "does not contain key")
	})

	t.Run("get outside subset errors", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(artifactsCallFail("modules", "get", "--key", "./app", "tests",
				"subset", "--keys", "TestAppAdd",
				"get", "--key", "TestAppGreet", "name")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "does not contain key")
	})
}

func (WorkspaceArtifactsSuite) TestBatch(_ context.Context, t *testctx.T) {
	t.Run("item-level run", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerCall("modules", "get", "--key", "./app", "tests",
				"get", "--key", "TestAppGreet", "run")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "./app: go test -run '^TestAppGreet$'", strings.TrimSpace(out))
	})

	t.Run("batch sees full subset", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goTestWorkspace(t, c).
			With(daggerCall("modules", "get", "--key", "./app", "tests",
				"batch", "run")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "./app: go test -run '^(TestAppGreet|TestAppAdd)$'", strings.TrimSpace(out))
	})

	t.Run("batch sees narrowed subset in parent order", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// keys passed in reverse order: subset must preserve parent order
		out, err := goTestWorkspace(t, c).
			With(daggerCall("modules", "get", "--key", "./app", "tests",
				"subset", "--keys", "TestAppAdd,TestAppGreet",
				"batch", "run")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "./app: go test -run '^(TestAppGreet|TestAppAdd)$'", strings.TrimSpace(out))
	})
}

func (WorkspaceArtifactsSuite) TestMainObjectCollection(_ context.Context, t *testctx.T) {
	// A module whose main object is itself a collection.
	withConstructor := func(c *dagger.Client) *dagger.Container {
		return workspaceBase(t, c).
			WithNewFile("dagger.toml", `[modules.gotests]
source = "gotests"
entrypoint = true
`).
			WithNewFile("gotests/dagger.json", `{"name": "gotests", "sdk": "go", "source": "."}`).
			WithNewFile("gotests/main.go", `package main

// A collection of tests as the module's main object
// +collection
type Gotests struct {
	Keys []string
}

func New() *Gotests {
	return &Gotests{Keys: []string{"TestA", "TestB"}}
}

// Select one test by name
func (t *Gotests) Get(name string) *TestItem {
	return &TestItem{Name: name}
}

// A single test
type TestItem struct {
	Name string
}
`)
	}

	t.Run("constructor populates keys", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := withConstructor(c).
			With(daggerCall("keys")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"TestA", "TestB"}, strings.Fields(out))
	})

	t.Run("subset and get work on main object", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := withConstructor(c).
			With(daggerCall("subset", "--keys", "TestB", "keys")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"TestB"}, strings.Fields(out))
	})

	t.Run("default constructor does not panic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// No New(): the engine installs a default constructor. Keys are
		// empty, but key validation must fail cleanly, not crash.
		out, err := workspaceBase(t, c).
			WithNewFile("dagger.toml", `[modules.gotests]
source = "gotests"
entrypoint = true
`).
			WithNewFile("gotests/dagger.json", `{"name": "gotests", "sdk": "go", "source": "."}`).
			WithNewFile("gotests/main.go", `package main

// A collection of tests as the module's main object
// +collection
type Gotests struct {
	Keys []string
}

// Select one test by name
func (t *Gotests) Get(name string) *TestItem {
	return &TestItem{Name: name}
}

// A single test
type TestItem struct {
	Name string
}
`).
			With(artifactsCallFail("get", "--key", "nope", "name")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "does not contain key")
	})
}

func (WorkspaceArtifactsSuite) TestStaticListing(_ context.Context, t *testctx.T) {
	// A module whose keys enumeration always fails: dimension and type
	// discovery must still work because they never run module code, and
	// value listing must degrade rather than fail the whole scope.
	brokenWorkspace := func(c *dagger.Client) *dagger.Container {
		return workspaceBase(t, c).
			WithNewFile("dagger.toml", `[modules.broken]
source = "broken"
entrypoint = true
`).
			WithNewFile("broken/dagger.json", `{"name": "broken", "sdk": "go", "source": "."}`).
			WithNewFile("broken/main.go", `package main

import (
	"context"
	"fmt"
)

type Broken struct{}

// A collection whose enumeration always fails
func (m *Broken) Things(ctx context.Context) (*Things, error) {
	return nil, fmt.Errorf("enumeration exploded")
}

// A collection of things
// +collection
type Things struct {
	Keys []string
}

// Select one thing
func (t *Things) Get(name string) *Thing {
	return &Thing{Name: name}
}

// A single thing
type Thing struct {
	Name string
}
`)
	}

	t.Run("dimensions without running module code", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := brokenWorkspace(c).
			With(daggerExecRaw("list")).
			Stdout(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"types", "broken-thing"}, strings.Fields(out))
	})

	t.Run("types without running module code", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := brokenWorkspace(c).
			With(daggerExecRaw("list", "types")).
			Stdout(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"broken", "broken-thing"}, strings.Fields(out))
	})

	t.Run("value listing degrades on enumeration failure", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := brokenWorkspace(c).
			With(daggerExecRaw("list", "broken-thing")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, strings.Fields(out))
	})
}
