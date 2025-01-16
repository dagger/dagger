package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ShellSuite struct{}

func TestShell(t *testing.T) {
	testctx.Run(testCtx, t, ShellSuite{}, Middleware()...)
}

func daggerShell(script string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger", "shell", "-c", script}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerShellNoMod(script string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger", "shell", "--no-mod", "-c", script}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
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
		With(daggerShell("container | with-exec cat,/etc/os-release | stdout")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Alpine Linux")
}

func (ShellSuite) TestModuleLookup(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	setup := modInit(t, c, "go", `// Main module

package main

import (
	"dagger/test/internal/dagger"
)

func New(
	// +defaultPath=.
	source *dagger.Directory,
) *Test {
	return &Test{Source: source}
}

// Test main object
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
		With(withModInitAt("other", "go", `// A local module

package main

type Other struct{}

func (Other) Version() string {
	return "other function"
} 
`,
		)).
		With(daggerExec("install", "./modules/dep")).
		With(daggerExec("install", "./modules/git")).
		With(daggerExec("install", "./modules/go"))

	t.Run("current module doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "MODULE")
		require.Contains(t, out, "Main module")
		require.Contains(t, out, "ENTRYPOINT")
		require.Contains(t, out, "Usage: . [options]")
		require.Contains(t, out, "AVAILABLE FUNCTIONS")
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
			With(daggerShell(".doc go")).
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
			With(daggerShell(".deps | .doc go")).
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
			With(daggerShell(".doc version")).
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
			With(daggerShell(".stdlib | .doc version")).
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
			With(daggerShell(".doc git")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "A git helper")
		require.Contains(t, out, "ENTRYPOINT")
		require.NotContains(t, out, "AVAILABLE FUNCTIONS")
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
			With(daggerShell(".doc other")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "A local module")
		require.NotContains(t, out, "ENTRYPOINT")
		require.NotContains(t, out, "AVAILABLE FUNCTIONS")
	})

	t.Run("current module required constructor arg error", func(ctx context.Context, t *testctx.T) {
		_, err := setup.
			WithWorkdir("modules/git").
			With(daggerShell("url")).
			Sync(ctx)
		requireErrOut(t, err, "constructor: missing 1 positional argument")
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
		require.JSONEq(t, `{"version": "dep function"}`, out)
	})

	t.Run("dep doc type", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell("dep | .doc")).
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
			With(daggerShell(".deps | .doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`\n  dep +Dependency module`), out)
		require.Regexp(t, regexp.MustCompile(`\n  git +A git helper`), out)
		require.Regexp(t, regexp.MustCompile(`\n  go +A go helper`), out)
	})

	t.Run("deps doc module", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".deps | .doc go")).
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
		require.Contains(t, out, "version")
		require.NotContains(t, out, "Get the current Dagger Engine version")
		require.NotContains(t, out, "load-container-from-id")
	})

	t.Run("stdlib doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".stdlib | .doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "version")
		require.Contains(t, out, "Get the current Dagger Engine version")
		require.NotContains(t, out, "load-container-from-id")
	})

	t.Run("stdlib doc function", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".stdlib | .doc git")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`^Queries a Git repository`), out)
		require.Contains(t, out, "git <url> [options]")
		require.Contains(t, out, "RETURNS")
	})

	t.Run("core result", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".core")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "- load-container-from-id")
		require.NotContains(t, out, "Load a Container")
	})

	t.Run("core doc", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".core | .doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "load-container-from-id")
		require.Contains(t, out, "Load a Container from its ID")
	})

	t.Run("core doc function", func(ctx context.Context, t *testctx.T) {
		out, err := setup.
			With(daggerShell(".core | .doc load-container-from-id")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, regexp.MustCompile(`^Load a Container from its ID`), out)
		require.Contains(t, out, "load-container-from-id <id>")
		require.Contains(t, out, "RETURNS")
	})
}

func (ShellSuite) TestNoModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := daggerCliBase(t, c)

	t.Run("module builtin does not work", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell(".deps")).Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})

	t.Run("no default module doc", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerShell(".doc")).Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})
}

func (ShellSuite) TestNoLoadModule(ctx context.Context, t *testctx.T) {
	t.Run("sanity check", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShell(".doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})

	t.Run("forced no load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		_, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(".deps")).
			Sync(ctx)
		requireErrOut(t, err, "module not loaded")
	})

	t.Run("dynamically loaded", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(".use .; .doc")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container-echo")
	})

	t.Run("stateless load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(". | .doc container-echo")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "echoes whatever string argument")
	})

	t.Run("stateless .doc load", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", "").
			With(daggerShellNoMod(".doc .")).
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
		require.JSONEq(t, `{"bar": "foobar"}`, out)
	})

	t.Run("stateful", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := modInit(t, c, "go", test).
			With(daggerExec("init", "--sdk=go", "--source=foo", "foo")).
			With(sdkSourceAt("foo", "go", foo)).
			With(daggerShell(".use foo; bar")).
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
	requireErrOut(t, err, "not found")
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
		require.Contains(t, out, "foo")
	})

	t.Run("pipeline from state value", func(ctx context.Context, t *testctx.T) {
		script := setup + "$FOO | file foo | contents"

		c := connect(ctx, t)
		out, err := daggerCliBase(t, c).With(daggerShell(script)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", out)
	})
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
	} {
		t.Run(strings.TrimSpace(tc.expected), func(ctx context.Context, t *testctx.T) {
			script := fmt.Sprintf("container | from %s | %s", alpineImage, tc.command)
			out, err := modGen.With(daggerShell(script)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expected, out)
		})
	}
}

func (ShellSuite) TestSliceFlag(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	script := fmt.Sprintf("directory | with-directory / $(container | from %s | directory /etc) --include=passwd,shadow | entries", alpineImage)
	out, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "passwd\nshadow\n", out)
}

func (ShellSuite) TestCommandStateArgs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	script := fmt.Sprintf("FOO=$(container | from %s | with-exec -- echo -n foo | stdout); .doc $FOO", alpineImage)
	_, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		Sync(ctx)
	requireErrOut(t, err, `"foo" not found`)
}

func (ShellSuite) TestExecStderr(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	script := fmt.Sprintf("container | from %s | with-exec ls wat | stdout", alpineImage)
	_, err := daggerCliBase(t, c).
		With(daggerShell(script)).
		Sync(ctx)
	requireErrOut(t, err, "ls: wat: No such file or directory")
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
