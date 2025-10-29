package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ShellSuite struct{}

func TestShell(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ShellSuite{})
}

func daggerShell(script string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger"}, dagger.ContainerWithExecOpts{
			Stdin:                         script,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerShellNoMod(script string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger", "-M"}, dagger.ContainerWithExecOpts{
			Stdin:                         script,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func (ShellSuite) TestScriptMode(ctx context.Context, t *testctx.T) {
	t.Run("root script argument", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			WithNewFile("script.sh", ".echo foobar").
			WithExec([]string{"dagger", "script.sh"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar\n", out)
	})

	t.Run("shell script argument", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			WithNewFile("script.sh", ".echo foobar").
			WithExec([]string{"dagger", "shell", "script.sh"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar\n", out)
	})

	t.Run("shell script shebang", func(ctx context.Context, t *testctx.T) {
		script := fmt.Sprintf("#!%s shell\n\n.echo foobar", testCLIBinPath)
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			WithNewFile("script.sh", script, dagger.ContainerWithNewFileOpts{
				Permissions: 0750,
			}).
			WithExec([]string{"./script.sh"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar\n", out)
	})

	t.Run("root script shebang", func(ctx context.Context, t *testctx.T) {
		script := fmt.Sprintf("#!%s\n\n.echo foobar", testCLIBinPath)
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			WithNewFile("script.sh", script, dagger.ContainerWithNewFileOpts{
				Permissions: 0750,
			}).
			WithExec([]string{"./script.sh"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar\n", out)
	})

	t.Run("root error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		_, err := daggerCliBase(t, c).
			WithExec([]string{"dagger", "qery"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Sync(ctx)
		requireErrOut(t, err, `unknown command or file "qery" for "dagger"`)
		requireErrOut(t, err, "Did you mean this?\n\tquery")
	})
}

func (ShellSuite) TestDefaultToModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := modInit(t, c, "go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Container() *dagger.Container {
	return dag.Container(). From("`+alpineImage+`")
}
`,
	).
		With(daggerShell("container | with-exec cat /etc/os-release | stdout")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Alpine Linux")
}

func (ShellSuite) TestModuleLookup(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	setup := modInit(t, c, "go", `// Main module
//
// Multiline module description.

package main

import (
	"dagger/test/internal/dagger"
)

// Constructor description.
func New(
	// +defaultPath=.
	source *dagger.Directory,
) *Test {
	return &Test{Source: source}
}

// Test main object
//
// Multiline object description.
type Test struct{
	Source *dagger.Directory
}

// Test version
func (Test) Version() string {
	return "test function"
}

// Encouragement
func (Test) Go() string {
	return "Let's go!"
}
`,
	).
		With(withModInitAt("modules/dep", "go", `// Dependency module

package main

func New() *Dep {
	return &Dep{
		Version: "dep function",
	}
}

type Dep struct{
	// Dep version
	Version string
}
`,
		)).
		With(withModInitAt("modules/git", "go", `// A git helper

package main

func New(url string) *Git {
	return &Git{URL: url}
}

type Git struct{
	URL string
}
`,
		)).
		With(withModInitAt("modules/go", "go", `// A go helper

package main

type Go struct{}

// Go version
func (Go) Version() string {
	return "go version"
}
`,
		)).
		With(withModInitAt("other", "go", ` package main

// A local module
type Other struct{}

func (Other) Version() string {
	return "other function"
}
`,
		)).
		With(daggerExec("install", "./modules/dep")).
		With(daggerExec("install", "./modules/git")).
		With(daggerExec("install", "./modules/go"))

	t.Run("general help", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Main module")
		require.NotContains(t, out, "Multiline module description.")
		require.Regexp(t, `version\s+Test version`, out)
		require.Regexp(t, `go\s+Encouragement`, out)
		require.Regexp(t, `dep\s+Dependency module`, out)
		require.Regexp(t, `git\s+A git helper`, out)
		require.NotContains(t, out, "A go helper")
	})

	t.Run("current module doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".help .")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "MODULE")
		require.Contains(t, out, "Main module")
		require.Contains(t, out, "Multiline module description.")
		require.Contains(t, out, "ENTRYPOINT")
		require.Contains(t, out, "Constructor description.")
		require.Contains(t, out, "Usage: . [options]")
		// functions
		require.Contains(t, out, "Test version")
		require.Contains(t, out, "Encouragement")
		// object description is only used as fallback
		require.NotContains(t, out, "Test main object.")
	})

	t.Run("current main object doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(". | .help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "OBJECT")
		require.Contains(t, out, "Test main object")
		require.Contains(t, out, "Multiline object description")
		// functions
		require.Contains(t, out, "Test version")
		require.Contains(t, out, "Encouragement")
	})

	t.Run("current module function takes precedence over dependency", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell("go")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Let's go!", out)
	})

	t.Run("current module function doc takes precedence over dependency", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".help go")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Encouragement")
		require.Contains(t, out, "RETURNS")
	})

	t.Run("disambiguate dependency function", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".deps | go | version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "go version", out)
	})

	t.Run("disambiguate dependency function doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".deps | .help go")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "MODULE")
		require.Contains(t, out, "A go helper")
	})

	t.Run("current module function takes precedence over stdlib", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell("version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "test function")
	})

	t.Run("current module function doc takes precedence over stdlib", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".help version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Test version")
	})

	t.Run("disambiguate stdlib command", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".stdlib | version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`^v0.\d+`), out)
	})

	t.Run("disambiguate stdlib command doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".stdlib | .help version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Get the current Dagger Engine version.")
		require.Contains(t, out, "RETURNS")
	})

	t.Run("dependency module function takes precedence over stdlib", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell("git acme.org | url")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "acme.org", out)
	})

	t.Run("dependency module function doc takes precedence over stdlib", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".help git")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "A git helper")
		require.Contains(t, out, "ENTRYPOINT")
		require.Contains(t, out, "AVAILABLE FUNCTIONS")
	})

	t.Run("other module function", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell("other | version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "other function", out)
	})

	t.Run("other module doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".help other")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "A local module") // main object description fallback
		require.NotContains(t, out, "ENTRYPOINT")
		require.Contains(t, out, "AVAILABLE FUNCTIONS")
	})

	t.Run("current module required constructor arg error", func(ctx context.Context, t *testctx.T) {
		_, err := setup.
			WithWorkdir("modules/git").
			With(daggerShell("url")).
			Sync(ctx)
		requireErrOut(t, err, "constructor: requires 1 positional argument(s), received 0")
	})

	t.Run("current module required constructor arg function", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			WithWorkdir("modules/git").
			With(daggerShell(". acme.org | url")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "acme.org", out)
	})

	t.Run("dep result", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell("dep")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `Dep@xxh3:[a-f0-9]{16}`, out)
	})

	t.Run("dep doc type", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell("dep | .help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "OBJECT")
		require.Contains(t, out, "\n  Dep\n")
		require.Regexp(t, regexp.MustCompile("\n  version +Dep version"), out)
	})

	t.Run("deps result", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".deps")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "- dep")
		require.Contains(t, out, "- git")
		require.Contains(t, out, "- go")
	})

	t.Run("deps doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".deps | .help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`\n  dep +Dependency module`), out)
		require.Regexp(t, regexp.MustCompile(`\n  git +A git helper`), out)
		require.Regexp(t, regexp.MustCompile(`\n  go +A go helper`), out)
	})

	t.Run("deps doc module", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".deps | .help go")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "MODULE")
		require.Contains(t, out, "A go helper")
	})

	t.Run("stdlib result", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".stdlib")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "- version")
		require.NotContains(t, out, "Get the current Dagger Engine version")
		require.NotContains(t, out, "- load-container-from-id")
		require.NotContains(t, out, "- git") // replaced by dependency
	})

	t.Run("stdlib doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".stdlib | .help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "version")
		require.Contains(t, out, "Get the current Dagger Engine version")
		require.NotContains(t, out, "load-container-from-id")
		require.NotContains(t, out, "Git repository") // replaced by dependency
	})

	t.Run("stdlib doc with function overridden by module constructor", func(ctx context.Context, t *testctx.T) {
		_, err := setup.
			With(daggerShell(".stdlib | .help git")).
			Stdout(ctx)
		require.Error(t, err)
		requireErrRegexp(t, err, "command not found")
	})

	t.Run("stdlib doc with function", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".stdlib | .help http")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "an http remote url content")
		require.Contains(t, out, "http <url> [options]")
		require.Contains(t, out, "RETURNS")
	})

	t.Run("core result", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".core")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "- load-container-from-id")
		require.NotContains(t, out, "Load a Container") // no descriptions
		require.NotContains(t, out, "- git")            // replaced by dependency
	})

	t.Run("core doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".core | .help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "load-container-from-id")
		require.Contains(t, out, "Load a Container from its ID")
		require.NotContains(t, out, "- git") // replaced by dependency
	})

	t.Run("core doc function", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".core | .help load-container-from-id")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`^Load a Container from its ID`), out)
		require.Contains(t, out, "load-container-from-id <id>")
		require.Contains(t, out, "RETURNS")
	})

	t.Run("stdlib with function overridden by module constructor", func(ctx context.Context, t *testctx.T) {
		_, err := setup.
			With(daggerShell(".stdlib | git")).
			Sync(ctx)
		requireErrOut(t, err, `command not found: "git"`)
	})

	t.Run("stdlib doc with function overridden by module constructor", func(ctx context.Context, t *testctx.T) {
		_, err := setup.
			With(daggerShell(".stdlib | .help git")).
			Sync(ctx)
		requireErrOut(t, err, `command not found: "git"`)
	})

	t.Run("core with function overridden by module constructor", func(ctx context.Context, t *testctx.T) {
		_, err := setup.
			With(daggerShell(".core | git")).
			Sync(ctx)
		requireErrOut(t, err, `"git" not found`)
	})

	t.Run("core doc with function overridden by module constructor", func(ctx context.Context, t *testctx.T) {
		_, err := setup.
			With(daggerShell(".core | .help git")).
			Sync(ctx)
		requireErrOut(t, err, `"git" not found`)
	})

	t.Run("types result", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".types")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "An OCI-compatible container")
		require.Contains(t, out, "A directory")
		require.Contains(t, out, "Test main object")
	})

	t.Run("doc Test type", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".help Test")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "OBJECT")
		require.Contains(t, out, "Test main object")
		require.Contains(t, out, "Encouragement")
	})
}

func (ShellSuite) TestNoModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := daggerCliBase(t, c)

	t.Run("module builtin does not work", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell(".deps")).Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})
}

func (ShellSuite) TestNoLoadModule(ctx context.Context, t *testctx.T) {
	t.Run("sanity check", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShell(".help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})

	t.Run("forced no load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(".help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "container-echo")
	})

	t.Run("dynamically loaded", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(".cd .; .help")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})

	t.Run("stateless load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(". | .help container-echo")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "echoes whatever string argument")
	})

	t.Run("stateless .help load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(".help .")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "MODULE")
		require.Contains(t, out, "A generated module for Test functions")
	})
}

func (ShellSuite) TestLoadAnotherModule(ctx context.Context, t *testctx.T) {
	test := `package main

type Test struct{}

func (m *Test) Bar() string {
	return "testbar"
}
`

	foo := `package main

func New() *Foo {
	return &Foo{
		Bar: "foobar",
	}
}

type Foo struct{
	Bar string
}
`
	t.Run("main object", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", test).
			With(daggerExec("init", "--sdk=go", "--source=foo", "foo")).
			With(sdkSourceAt("foo", "go", foo)).
			With(daggerShell("foo")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `Foo@xxh3:[a-f0-9]{16}`, out)
	})

	t.Run("stateful", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", test).
			With(daggerExec("init", "--sdk=go", "--source=foo", "foo")).
			With(sdkSourceAt("foo", "go", foo)).
			With(daggerShell(".cd foo; bar")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "foobar")
	})

	t.Run("stateless", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := modInit(t, c, "go", test).
			With(daggerExec("init", "--sdk=go", "--source=foo", "foo")).
			With(sdkSourceAt("foo", "go", foo))

		out, err := modGen.
			With(daggerShell("foo | bar")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar", out)

		out, err = modGen.
			With(daggerShell("bar")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "testbar", out)
	})
}

func (ShellSuite) TestNotExists(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	_, err := modInit(t, c, "go", "").
		With(daggerShell("load-container-from-id")).
		Sync(ctx)
	requireErrOut(t, err, "\"load-container-from-id\" does not exist")
}

func (ShellSuite) TestIntegerArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := "container | with-exposed-port 80 | exposed-ports | port"
	out, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "80\n", out)
}

func (ShellSuite) TestExport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := "directory | with-new-file foo bar | export mydir"
	out, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		File("mydir/foo").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "bar", out)
}

func (ShellSuite) TestBasicModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	script := "container-echo hello-world-im-here | stdout"
	out, err := daggerCliBase(t, c).
		With(withModInit("go", "")).
		With(daggerShell(script)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-world-im-here")
}

func (ShellSuite) TestPassingID(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	source := `package main

import "context"

type Test struct{}

func (m *Test) DirectoryID(ctx context.Context) (string, error) {
	id, err := dag.Directory().WithNewFile("foo", "bar").ID(ctx)
	return string(id), err
}
`
	script := ".core | load-directory-from-id $(directory-id) | file foo | contents"

	out, err := modInit(t, c, "go", source).
		With(daggerShell(script)).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "bar", out)
}

func (ShellSuite) TestStateCommand(ctx context.Context, t *testctx.T) {
	setup := "FOO=$(directory | with-new-file foo bar); "

	t.Run("state result", func(ctx context.Context, t *testctx.T) {
		script := setup + "$FOO"

		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).With(daggerShell(script)).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `Directory@xxh3:[a-f0-9]{16}`, out)
	})

	t.Run("pipeline from state value", func(ctx context.Context, t *testctx.T) {
		script := setup + "$FOO | file foo | contents"

		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).With(daggerShell(script)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", out)
	})
}

func (ShellSuite) TestStateInVarUsage(ctx context.Context, t *testctx.T) {
	script := `
dir=$(directory | with-new-file foo bar)
$dir | name
$dir | entries
`
	c := connect(ctx, t)
	out, err := daggerCliBase(t, c).With(daggerShellNoMod(script)).Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, out, "/")
	require.Contains(t, out, "foo")
}

func (ShellSuite) TestStateVarImmutability(ctx context.Context, t *testctx.T) {
	script := `
dir=$(directory | with-new-file /src/foo/bar foobar | directory src)
name=$($dir | name)
.printenv name               # src/\n
$dir | directory foo | name  # foo/
.printenv name               # src/\n
`
	c := connect(ctx, t)
	out, err := daggerCliBase(t, c).With(daggerShellNoMod(script)).Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "src/\nfoo/src/\n", out)
}

func (ShellSuite) TestArgsSpread(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := daggerCliBase(t, c)

	for _, tc := range []struct {
		command  string
		expected string
	}{
		{
			command:  `with-exec echo hello world | stdout`,
			expected: "hello world\n",
		},
		{
			command:  `with-exec echo with tail options --redirect-stdout /out | file /out | contents`,
			expected: "with tail options\n",
		},
		{
			command:  `with-exec --redirect-stdout /out echo with head options | file /out | contents`,
			expected: "with head options\n",
		},
		{
			command:  `with-exec --stdin "with interspersed args" cat --redirect-stdout /out | file /out | contents`,
			expected: "with interspersed args",
		},
		{
			command:  `with-exec --redirect-stdout /out -- echo -n stop processing flags: --expand | file /out | contents`,
			expected: "stop processing flags: --expand",
		},
		{
			command:  `with-env-variable MSG "with double" | with-exec --redirect-stdout /out --expand -- echo -n \$MSG --expand | file /out | contents`,
			expected: "with double --expand",
		},
		{
			command:  `with-exec --redirect-stdout /out -- echo -n git checkout -- file | file /out | contents`,
			expected: "git checkout -- file",
		},
		{
			command:  `with-exec -- sh -c 'echo hello something,with,commas' | stdout`,
			expected: "hello something,with,commas\n",
		},
		{
			command:  `with-exec -- sh -c 'echo "with double quotes"' | stdout`,
			expected: "with double quotes\n",
		},
		{
			command:  `with-exec -- sh -c "echo 'with single quotes'" | stdout`,
			expected: "with single quotes\n",
		},
		{
			command:  `with-exec -- sh -c "echo $(directory | with-new-file foo "with state" | file foo | contents)" | stdout`,
			expected: "with state\n",
		},
		{
			command:  `with-exec echo,with,csv,support | stdout`,
			expected: "with csv support\n",
		},
	} {
		t.Run(strings.TrimSpace(tc.expected), func(ctx context.Context, t *testctx.T) {
			script := fmt.Sprintf("container | from %s | %s", alpineImage, tc.command)
			out, err := modGen.With(daggerShell(script)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expected, out)
		})
	}
}

func (ShellSuite) TestDirectoryFlag(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		initial  string
		path     string
		expected string
	}{
		{
			initial:  ".",
			path:     "sub/a",
			expected: "foo\n",
		},
		{
			initial:  "sub/a",
			path:     ".",
			expected: "foo\n",
		},
		{
			initial:  "sub/a",
			path:     "../b",
			expected: "bar\n",
		},
		{
			initial:  "sub/a",
			path:     "../../ab",
			expected: "foobar\n",
		},
	} {
		t.Run(tc.initial+" - "+tc.path, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			out, err := goGitBase(t, c).
				WithNewFile("sub/a/foo", "foo").
				WithNewFile("sub/b/bar", "bar").
				WithNewFile("ab/foobar", "foobar").
				WithWorkdir(tc.initial).
				With(daggerShell(fmt.Sprintf("directory | with-directory / %s | entries", tc.path))).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expected, out)
		})
	}
}

func (ShellSuite) TestSliceFlag(ctx context.Context, t *testctx.T) {
	script := fmt.Sprintf("directory | with-directory / $(container | from %s | directory /etc) --include=passwd,shadow | entries", alpineImage)

	c := connect(ctx, t)
	out, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "passwd\nshadow\n", out)
}

func (ShellSuite) TestObjectSliceArgument(ctx context.Context, t *testctx.T) {
	script := fmt.Sprintf(`
arm64=$(container --platform linux/arm64 | from %[1]s)
amd64=$(container --platform linux/amd64 | from %[1]s)
container | export container.tar --platform-variants $arm64,$amd64
`, alpineImage)

	c := connect(ctx, t)
	size, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		File("container.tar").
		Size(ctx)

	require.NoError(t, err)
	require.Greater(t, size, 0)
}

func (ShellSuite) TestStateInterpolation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := daggerCliBase(t, c)

	for _, tc := range []struct {
		name     string
		prompt   string
		expected string
	}{
		{
			name:     "single state result",
			prompt:   `$($FOO | name)`,
			expected: "foo",
		},
		{
			name:     "interpolated command",
			prompt:   `.$($FOO | contents) hello`,
			expected: "hello\n",
		},
		{
			name:     "single state argument",
			prompt:   `directory | with-new-file test $($FOO | name) | file test | contents`,
			expected: "foo",
		},
		{
			name:     "multiple state argument",
			prompt:   `directory | with-new-file ./$($FOO | name)_$($BAR | name).txt foobar | entries`,
			expected: "foo_bar.txt\n",
		},
		{
			name:     "command argument",
			prompt:   `.ls ./$($FOO | name)/$($BAR | name)`,
			expected: "foobar.txt\n",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			script := []string{
				"FOO=$(directory | with-new-file foo echo | file foo)",
				"BAR=$(directory | with-new-file bar directory | file bar)",
			}
			out, err := modGen.
				WithNewFile("foo/bar/foobar.txt", "foobar").
				With(daggerShellNoMod(strings.Join(append(script, tc.prompt), "\n"))).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expected, out)
		})
	}

	// Don't use `_echo` because it'll just spit out whatever we give it
	// to stdout, which will be picked up by the state resolution at
	// the end of the run.
	for _, prefix := range []string{"_", "."} {
		t.Run("builtin argument with "+prefix, func(ctx context.Context, t *testctx.T) {
			script := prefix + "exit $(directory | with-new-file exit_code 5 | file exit_code | contents)"
			_, err := modGen.With(daggerShellNoMod(script)).Sync(ctx)
			var execErr *dagger.ExecError
			require.ErrorAs(t, err, &execErr)
			require.Equal(t, 5, execErr.ExitCode)
		})
	}
}

func (ShellSuite) TestCommandStateArgs(ctx context.Context, t *testctx.T) {
	cmd := rand.Text()
	script := fmt.Sprintf("FOO=$(container | from %s | with-exec -- echo -n %s | stdout); .help $FOO", alpineImage, cmd)

	c := connect(ctx, t)
	_, err := daggerCliBase(t, c).With(daggerShell(script)).Sync(ctx)

	requireErrOut(t, err, fmt.Sprintf("%q does not exist", cmd))
}

func (ShellSuite) TestStdlibStateArgs(ctx context.Context, t *testctx.T) {
	// Test an object argument on a stdlib command
	contents := rand.Text()
	script := fmt.Sprintf(`
svc=$(container | from %s | with-mounted-directory /srv . | with-workdir /srv | with-exposed-port 8000 | as-service --args="python,-m,http.server")
http http://$($svc | endpoint)/index.html --experimental-service-host $svc | contents
`, pythonImage)

	c := connect(ctx, t)
	out, err := daggerCliBase(t, c).
		WithNewFile("index.html", contents).
		With(daggerShell(script)).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, contents, out)
}

func (ShellSuite) TestExecStderr(ctx context.Context, t *testctx.T) {
	cmd := rand.Text()
	script := fmt.Sprintf("container | from %s | with-exec ls %s | stdout", alpineImage, cmd)

	c := connect(ctx, t)
	_, err := daggerCliBase(t, c).With(daggerShell(script)).Sync(ctx)

	requireErrOut(t, err, fmt.Sprintf("ls: %s: No such file or directory", cmd))
}

func (ShellSuite) TestExitCommand(ctx context.Context, t *testctx.T) {
	t.Run("specific code", func(ctx context.Context, t *testctx.T) {
		script := `directory | with-new-file foo foo | entries; .exit 5; .echo ok`

		c := connect(ctx, t)
		_, err := daggerCliBase(t, c).With(daggerShell(script)).Sync(ctx)

		var execErr *dagger.ExecError
		require.ErrorAs(t, err, &execErr)
		require.Equal(t, 5, execErr.ExitCode)
		require.Contains(t, execErr.Stdout, "foo")
		require.NotContains(t, execErr.Stdout, "ok")
	})

	t.Run("no args", func(ctx context.Context, t *testctx.T) {
		// without `set +e` it won't reach `.exit`
		script := `_set +e; directory | with-new-file | entries; .exit; .echo ok`

		c := connect(ctx, t)
		_, err := daggerCliBase(t, c).With(daggerShell(script)).Sync(ctx)

		var execErr *dagger.ExecError
		require.ErrorAs(t, err, &execErr)
		require.Equal(t, 1, execErr.ExitCode)
		require.NotContains(t, execErr.Stdout, "ok")
	})

	t.Run("no error", func(ctx context.Context, t *testctx.T) {
		// no error because `.echo ok` returns status code 0
		script := `_set +e; directory | with-new-file | entries; .echo ok; .exit; .echo exited`

		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).With(daggerShell(script)).Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "ok")
		require.NotContains(t, out, "exited")
	})
}

func (ShellSuite) TestExecExit(ctx context.Context, t *testctx.T) {
	msg := rand.Text()
	script := fmt.Sprintf(`container | from %s | with-exec -- sh -c ">&2 echo %q; exit 5" | stdout`, alpineImage, msg)

	c := connect(ctx, t)
	_, err := daggerCliBase(t, c).With(daggerShell(script)).Sync(ctx)

	var execErr *dagger.ExecError
	require.ErrorAs(t, err, &execErr)
	require.Equal(t, 5, execErr.ExitCode)
	require.Contains(t, execErr.Stderr, msg)
}

func (ShellSuite) TestNonExecChainBreak(ctx context.Context, t *testctx.T) {
	for i, tc := range []string{
		"directory | with-file",
		"directory | with-file | entries",
		"directory | with-file | with-directory | entries",
		"DIR=$(directory | with-file); $DIR",
		"DIR=$(directory | with-file); $DIR | entries",
		"directory | with-directory foo $(directory | with-file) | entries",
	} {
		t.Run(fmt.Sprintf("case %d", i), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			_, err := daggerCliBase(t, c).With(daggerShell(tc)).Sync(ctx)

			requireErrRegexp(t, err, `requires 2 positional argument.*\nusage: with-file <path> <source>`)
		})
	}
}

func (ShellSuite) TestInstall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := modInit(t, c, "go", "").
		With(daggerExec("init", "--sdk=go", "dep")).
		With(daggerShell(".install dep")).
		WithExec([]string{"grep", "dep", "dagger.json"}).
		Sync(ctx)

	require.NoError(t, err)
}

func (ShellSuite) TestMultipleCommandOutputs(ctx context.Context, t *testctx.T) {
	t.Run("sync", func(ctx context.Context, t *testctx.T) {
		script := `
directory | with-new-file test foo | file test | contents
directory | with-new-file test bar | file test | contents
`
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			With(daggerShell(script)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar", out)
	})

	t.Run("async", func(ctx context.Context, t *testctx.T) {
		script := `
directory | with-new-file test foo | file test | contents &
directory | with-new-file test bar | file test | contents & 
.wait
`
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			With(daggerShell(script)).
			Stdout(ctx)
		require.NoError(t, err)

		if out != "foobar" && out != "barfoo" {
			t.Errorf("unexpected output: %q", out)
		}
	})

	t.Run("async error", func(ctx context.Context, t *testctx.T) {
		script := fmt.Sprintf(`
container | from %[1]s | with-exec -- sh -c 'exit 5' | stdout &
job1=$!
container | from %[1]s | with-exec false | stdout &
job2=$!
container | from %[1]s | with-exec echo ok | stdout &
job3=$!
.wait $job1 $job2 $job3
`, alpineImage,
		)

		c := connect(ctx, t)
		_, err := daggerCliBase(t, c).With(daggerShell(script)).Sync(ctx)

		// should exit with the same exit code as the first failed command
		var ex *dagger.ExecError
		require.ErrorAs(t, err, &ex)
		require.Equal(t, 5, ex.ExitCode)
	})

	t.Run("async error no pids", func(ctx context.Context, t *testctx.T) {
		script := fmt.Sprintf(`
container | from %[1]s | with-exec false | stdout &
container | from %[1]s | with-exec -- sh -c 'exit 5' | stdout &
container | from %[1]s | with-exec echo ok | stdout &
.wait
`, alpineImage,
		)

		c := connect(ctx, t)
		_, err := daggerCliBase(t, c).With(daggerShell(script)).Sync(ctx)

		// Without arguments, .wait always returns zero (successfully waited
		// for all jobs to finish)
		require.NoError(t, err)
	})
}

func (ShellSuite) TestInterpreterBuiltins(ctx context.Context, t *testctx.T) {
	t.Run("internal", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			With(daggerShell(`_echo foobar`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar\n", out)
	})

	t.Run("exposed", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			With(daggerShell(`.echo foobar`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar\n", out)
	})

	t.Run("reserved", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		_, err := daggerCliBase(t, c).
			With(daggerShell(`__dag`)).
			Sync(ctx)
		requireErrOut(t, err, "reserved for internal use")
	})

	t.Run("unknown", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		_, err := daggerCliBase(t, c).
			With(daggerShell(`_container`)).
			Sync(ctx)
		requireErrOut(t, err, "does not exist")
	})
}

func (ShellSuite) TestPrintenvCommand(ctx context.Context, t *testctx.T) {
	t.Run("printenv all", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			With(daggerShell(`.printenv`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "GOPATH=/go")
		require.Contains(t, out, "HOME=/root")
		require.Contains(t, out, "PWD=/work")
	})

	t.Run("printenv specific", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			With(daggerShell(`.printenv PATH`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "/usr/local/bin")
	})

	t.Run("printenv non-existing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		_, err := daggerCliBase(t, c).
			With(daggerShell(`.printenv NON_EXISTING_VAR`)).
			Sync(ctx)
		requireErrOut(t, err, `environment variable "NON_EXISTING_VAR" not set`)
	})

	t.Run("printenv shows set envs", func(ctx context.Context, t *testctx.T) {
		script := `
ctr=$(container)
.printenv ctr
`
		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).
			With(daggerShell(script)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Container@xxh3:")
	})
}

func (ShellSuite) TestNamedArguments(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("named arguments for required parameters", func(ctx context.Context, t *testctx.T) {
		// Test with-exec using named arguments for args parameter
		out, err := daggerCliBase(t, c).
			With(daggerShell(`container | from alpine | with-exec --args=echo --args=hello | stdout`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("named arguments for from function", func(ctx context.Context, t *testctx.T) {
		// Test from function using named argument for address parameter
		out, err := daggerCliBase(t, c).
			With(daggerShell(`container | from --address=alpine | with-exec echo test | stdout`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "test\n", out)
	})

	t.Run("mixed positional and named arguments", func(ctx context.Context, t *testctx.T) {
		// Test mixing positional and named arguments (positional first)
		out, err := daggerCliBase(t, c).
			With(daggerShell(`directory | with-new-file /test --contents="hello world" | file /test | contents`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello world", out)
	})

	t.Run("backward compatibility - all positional", func(ctx context.Context, t *testctx.T) {
		// Ensure original positional syntax still works
		out, err := daggerCliBase(t, c).
			With(daggerShell(`container | from alpine | with-exec echo "backward compatible" | stdout`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "backward compatible\n", out)
	})

	t.Run("slice arguments with multiple values", func(ctx context.Context, t *testctx.T) {
		// Test that slice arguments can accept multiple named values
		out, err := daggerCliBase(t, c).
			With(daggerShell(`container | from alpine | with-exec --args=sh --args=-c --args="echo 'multiple args'" | stdout`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "multiple args\n", out)
	})

	t.Run("error on mixed args incorrectly", func(ctx context.Context, t *testctx.T) {
		// Test error when mixing positional and named incorrectly
		_, err := daggerCliBase(t, c).
			With(daggerShell(`container | from alpine | with-exec echo --args=hello | stdout`)).
			Sync(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "requires 0 positional argument(s), received 1")
	})

	t.Run("all required args as named", func(ctx context.Context, t *testctx.T) {
		// Test providing all required arguments as named arguments
		out, err := daggerCliBase(t, c).
			With(daggerShell(`directory | with-new-file --path=/greeting --contents="Hello Named Args!" | file --path=/greeting | contents`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello Named Args!", out)
	})

	t.Run("named args with optional flags", func(ctx context.Context, t *testctx.T) {
		// Test named required args combined with optional flags
		out, err := daggerCliBase(t, c).
			With(daggerShell(`container | from alpine | with-exec --args=echo --args=test --redirect-stdout=/output | file /output | contents`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "test\n", out)
	})

	t.Run("order preservation with mixed args", func(ctx context.Context, t *testctx.T) {
		// Test that positional order is preserved when mixing args
		// This should be equivalent to: with-directory /src .
		out, err := daggerCliBase(t, c).
			With(daggerShell(`directory | with-new-file test.txt "content" | with-directory --path=/src $(directory) | entries`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "test.txt")
	})
}
