package core

// Workspace alignment: aligned structurally, but coverage is still incomplete.
// Scope: Context-derived workspace selection and find-up behavior when commands run from nested directories.
// Intent: Pin down contextual workspace inference separately from compat detection and generic module loading.

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// ContextualWorkspaceSuite owns how the ambient/default Workspace is inferred
// from invocation context. This includes whether a Workspace is injected at
// all, which workspace wins, and how cache invalidation works for that input.
type ContextualWorkspaceSuite struct{}

func TestContextualWorkspace(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ContextualWorkspaceSuite{})
}

func daggerReportCall(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerReportCallFailure(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

// TestContextualWorkspaceSelection should cover which workspace gets injected
// from context before any module code runs.
func (ContextualWorkspaceSuite) TestContextualWorkspaceSelection(ctx context.Context, t *testctx.T) {
	t.Run("initialized workspace is inferred from nearest config boundary", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "app"}).
			WithNewFile("repo.txt", "hello from boundary").
			WithNewFile("app/app.txt", "hello from workspace").
			WithWorkdir("/work/app").
			With(initDangBlueprint("paths", `
type Paths {
  pub workspaceValue: String!
  pub boundaryValue: String!
  pub foundValue: String!
  pub workspacePath: String!
  pub workspaceAddress: String!

  new(ws: Workspace!) {
    self.workspaceValue = ws.file("app.txt").contents
    self.boundaryValue = ws.file("/repo.txt").contents
    self.foundValue = ws.findUp(name: "repo.txt", from: ".") ?? ""
    self.workspacePath = ws.path
    self.workspaceAddress = ws.address
    self
  }
}
`))

		out, err := ctr.With(daggerReportCall("workspace-value")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from workspace", strings.TrimSpace(out))

		out, err = ctr.With(daggerReportCall("boundary-value")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from boundary", strings.TrimSpace(out))

		out, err = ctr.With(daggerReportCall("found-value")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/repo.txt", strings.TrimSpace(out))
	})

	t.Run("legacy compat workspace is inferred when no config exists", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("repo.txt", "hello from compat workspace").
				WithNewFile("ci/main.dang", `
type Myapp {
  pub workspacePath: String!
  pub workspaceAddress: String!
  pub boundaryValue: String!

  new(ws: Workspace!) {
    self.workspacePath = ws.path
    self.workspaceAddress = ws.address
    self.boundaryValue = ws.file("/repo.txt").contents
    self
  }
}
`)
		})

		out, err := ctr.With(daggerReportCall("workspace-path")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, ".", strings.TrimSpace(out))

		out, err = ctr.With(daggerReportCall("workspace-address")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file:///work", strings.TrimSpace(out))

		out, err = ctr.With(daggerReportCall("boundary-value")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from compat workspace", strings.TrimSpace(out))
	})

	t.Run("nearest initialized workspace beats an outer initialized workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := workspaceBase(t, c).
			WithNewFile(".dagger/config.toml", "[modules]\n").
			WithNewFile("app/.dagger/config.toml", "[modules]\n").
			WithWorkdir("/work/app").
			With(initDangBlueprint("paths", `
type Paths {
  pub workspacePath: String!
  pub workspaceAddress: String!

  new(ws: Workspace!) {
    self.workspacePath = ws.path
    self.workspaceAddress = ws.address
    self
  }
}
`))

		out, err := ctr.With(daggerReportCall("workspace-path")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "app", strings.TrimSpace(out))

		out, err = ctr.With(daggerReportCall("workspace-address")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file:///work/app", strings.TrimSpace(out))
	})

	t.Run("non-eligible legacy module does not inject a workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "standalone",
  "sdk": {"source": "dang"}
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile("main.dang", `
type Standalone {
  pub workspacePath: String!

  new(ws: Workspace!) {
    self.workspacePath = ws.path
    self
  }
}
`)
		})

		out, err := ctr.With(daggerReportCallFailure("workspace-path")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "no current workspace")
	})

	t.Run("initialized workspace beats outer compat inference", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "outer",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", `
type Outer {
  pub marker: String! {
    "outer compat module"
  }
}
`).
				WithNewFile("app/.dagger/config.toml", "[modules]\n").
				WithWorkdir("/work/app").
				With(initDangBlueprint("paths", `
type Paths {
  pub workspacePath: String!
  pub workspaceAddress: String!

  new(ws: Workspace!) {
    self.workspacePath = ws.path
    self.workspaceAddress = ws.address
    self
  }
}
`))
		})

		out, err := ctr.With(daggerReportCall("workspace-path")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "app", strings.TrimSpace(out))

		out, err = ctr.With(daggerReportCall("workspace-address")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file:///work/app", strings.TrimSpace(out))
	})
}

// TestContextualWorkspaceShape should pin down the observable properties of
// the injected Workspace once it has been selected.
func (ContextualWorkspaceSuite) TestContextualWorkspaceShape(ctx context.Context, t *testctx.T) {
	t.Run("workspace path and address reflect injected boundary", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "app"}).
			WithWorkdir("/work/app").
			With(initDangBlueprint("paths", `
type Paths {
  pub workspacePath: String!
  pub workspaceAddress: String!

  new(ws: Workspace!) {
    self.workspacePath = ws.path
    self.workspaceAddress = ws.address
    self
  }
}
`))

		out, err := ctr.With(daggerReportCall("workspace-path")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "app", strings.TrimSpace(out))

		out, err = ctr.With(daggerReportCall("workspace-address")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file:///work/app", strings.TrimSpace(out))
	})

	t.Run("workspace findUp is rooted at the injected boundary", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := workspaceBase(t, c).
			WithExec([]string{"mkdir", "-p", "app/nested"}).
			WithNewFile("app/nested/target.txt", "nested target").
			WithWorkdir("/work/app").
			With(initDangBlueprint("paths", `
type Paths {
  pub foundFromNested: String!
  pub missingFromBoundary: String!

  new(ws: Workspace!) {
    self.foundFromNested = ws.findUp(name: "target.txt", from: "nested") ?? ""
    self.missingFromBoundary = ws.findUp(name: "target.txt", from: "/") ?? ""
    self
  }
}
`))

		out, err := ctr.With(daggerReportCall("found-from-nested")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/app/nested/target.txt", strings.TrimSpace(out))

		out, err = ctr.With(daggerReportCall("missing-from-boundary")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

// TestContextualWorkspaceCaching should cover cache behavior for functions
// that receive a Workspace from ambient context.
func (ContextualWorkspaceSuite) TestContextualWorkspaceCaching(ctx context.Context, t *testctx.T) {
	var marker = "FUNCTION_EXECUTED:" + rand.Text()

	daggerCallWithLogs := func(args ...string) dagger.WithContainerFunc {
		return func(ctr *dagger.Container) *dagger.Container {
			execArgs := append([]string{"dagger", "--progress=logs", "call"}, args...)
			return ctr.WithExec(execArgs, dagger.ContainerWithExecOpts{
				UseEntrypoint: true,
			})
		}
	}

	t.Run("same relevant workspace content hits cache", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceBase(t, c).
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", exclude: ["*", "!included-file"])
    self
  }

  pub read: String! {
    print("`+marker+`")
    source.file("included-file").contents
  }
}
`))

		first := base.With(daggerCallWithLogs("cacheme", "read"))
		out1, err := first.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out1, marker)

		second := first.With(daggerCallWithLogs("cacheme", "read"))
		out2, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out2, marker)
	})

	t.Run("unrelated file changes do not invalidate scoped workspace inputs", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceBase(t, c).
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", exclude: ["*", "!included-file"])
    self
  }

  pub read: String! {
    print("`+marker+`")
    source.file("included-file").contents
  }
}
`))

		first := base.With(daggerCallWithLogs("cacheme", "read"))
		_, err := first.CombinedOutput(ctx)
		require.NoError(t, err)

		second := first.
			WithNewFile("another-file", rand.Text()).
			With(daggerCallWithLogs("cacheme", "read"))
		out, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, marker)
	})

	t.Run("relevant file changes invalidate cache", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		newText := rand.Text()
		base := workspaceBase(t, c).
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", exclude: ["*", "!included-file"])
    self
  }

  pub read: String! {
    print("`+marker+`")
    source.file("included-file").contents
  }
}
`))

		first := base.With(daggerCallWithLogs("cacheme", "read"))
		_, err := first.CombinedOutput(ctx)
		require.NoError(t, err)

		second := first.
			WithNewFile("included-file", newText).
			With(daggerCallWithLogs("cacheme", "read"))
		out, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, newText)
		require.Contains(t, out, marker)
	})
}

// TestContextualWorkspaceCLIExposure covers user-visible behavior that is
// specific to Workspace being injected from context rather than passed
// explicitly.
func (ContextualWorkspaceSuite) TestContextualWorkspaceCLIExposure(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("hello.txt", "hello from workspace").
		With(initDangModule("greeter", `
type Greeter {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub read: String! {
    source.file("hello.txt").contents
  }
}
`))

	t.Run("workspace arg is auto-injected", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("greeter", "read")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from workspace", strings.TrimSpace(out))
	})

	t.Run("workspace arg is not exposed as a CLI flag", func(ctx context.Context, t *testctx.T) {
		help, err := ctr.With(daggerReportCall("greeter", "--help")).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, help, "--source")
	})
}
