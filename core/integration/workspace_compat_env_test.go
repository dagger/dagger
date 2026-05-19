package core

// These tests cover legacy `.env` files that provide default argument values to
// modules in compat workspaces and old project layouts. They verify file,
// directory, scalar, list, and blueprint-driven defaults.
//
// See also:
// - workspace_config_test.go: current workspace module settings behavior.
// - workspace_env_management_test.go: named workspace environments.

import (
	"context"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func daggerCallCmd(modPath string, args ...string) []string {
	cmd := []string{"dagger", "call"}
	if modPath != "" {
		cmd = append(cmd, "-m", modPath)
	}
	return append(cmd, args...)
}

func (WorkspaceCompatSuite) TestRemoteFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithWorkdir("defaults").
		WithNewFile(".env", `DEFAULTS_FILE=https://github.com/dagger/dagger#main:cmd/dagger/main.go`).
		WithExec(daggerCallCmd(".", "file", "contents"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, output, "package main")
}

func (WorkspaceCompatSuite) TestLocalFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile("hello.txt", "well hello!").
		WithWorkdir("defaults").
		WithNewFile(".env", `DEFAULTS_FILE=../hello.txt`).
		WithExec(daggerCallCmd(".", "file", "contents"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "well hello!", output)
}

func (WorkspaceCompatSuite) TestLocalDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithDirectory("data", dag.Directory().WithNewFile("hello.txt", "well hello!")).
		WithWorkdir("defaults").
		WithNewFile(".env", `DEFAULTS_DIR=../data`).
		WithExec(daggerCallCmd(".", "dir", "file", "--path=hello.txt", "contents"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "well hello!", output)
}

func (WorkspaceCompatSuite) TestRemoteDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithWorkdir("defaults").
		WithNewFile(".env", `DIR=https://github.com/dagger/dagger#main:cmd/dagger`).
		WithExec(daggerCallCmd(".", "dir", "file", "--path=main.go", "contents"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, output, "package main")
}

// TestCompatBlueprintDefaults keeps coverage for legacy blueprint-driven
// .env resolution in compat mode.
func (WorkspaceCompatSuite) TestCompatBlueprintDefaults(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile("./app/dagger.json", `{"name":"app", "blueprint": {"name":"defaults", "source":"../defaults"}}`)
	for _, tc := range []struct {
		description    string
		dotEnvPath     string
		dotEnvContents string
		workdir        string
		command        []string
		expect         dagger.ReturnType
		stdout         string
	}{
		{
			"inner envfile",
			"./app/.env",
			`
		GREETING=salut-inner
		MESSAGE_NAME=monde-inner
		`,
			"./app",
			[]string{"dagger", "call", "message"},
			dagger.ReturnTypeSuccess,
			"salut-inner, monde-inner!",
		},
		{
			"outer envfile inner workdir",
			".env",
			`
		DEFAULTS_GREETING=bonjour-outer
		DEFAULTS_MESSAGE_NAME=monde-outer
		`,
			"./app",
			[]string{"dagger", "call", "message"},
			dagger.ReturnTypeSuccess,
			"bonjour-outer, monde-outer!",
		},
		{
			"outer envfile outer workdir",
			".env",
			`
DEFAULTS_GREETING=salutations-outer
DEFAULTS_MESSAGE_NAME=planete-outer
`,
			"",
			// The app dagger.json contains legacy workspace fields, so an
			// explicit outer-workdir invocation must select it as a workspace.
			// Direct `-m ./app` loading is covered by the legacy direct-load
			// error tests.
			[]string{"dagger", "-W", "./app", "call", "message"},
			dagger.ReturnTypeSuccess,
			"salutations-outer, planete-outer!",
		},
	} {
		t.Run(tc.description, func(ctx context.Context, t *testctx.T) {
			stdout, err := ctr.
				WithNewFile(tc.dotEnvPath, tc.dotEnvContents).
				With(func(c *dagger.Container) *dagger.Container {
					if tc.workdir != "" {
						return c.WithWorkdir(tc.workdir)
					}
					return c
				}).
				WithExec(tc.command, dagger.ContainerWithExecOpts{
					Expect:                        tc.expect,
					ExperimentalPrivilegedNesting: true,
				}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.stdout, stdout)
		})
	}
}

// TestCompatToolchainDefaults keeps coverage for legacy toolchain-driven
// .env resolution in compat mode.
func (WorkspaceCompatSuite) TestCompatToolchainDefaults(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile("./app/dagger.json", `{"name":"app", "toolchains": [{"name":"defaults", "source":"../defaults"}]}`)
	for _, tc := range []struct {
		description    string
		dotEnvPath     string
		dotEnvContents string
		workdir        string
		command        []string
		expect         dagger.ReturnType
		stdout         string
	}{
		{
			"inner envfile",
			"./app/.env",
			`
		DEFAULTS_GREETING=salut-inner
		DEFAULTS_MESSAGE_NAME=monde-inner
		`,
			"./app",
			[]string{"dagger", "call", "defaults", "message"},
			dagger.ReturnTypeSuccess,
			"salut-inner, monde-inner!",
		},
		{
			"outer envfile inner workdir",
			".env",
			`
		DEFAULTS_GREETING=bonjour-outer
		DEFAULTS_MESSAGE_NAME=monde-outer
		`,
			"./app",
			[]string{"dagger", "call", "defaults", "message"},
			dagger.ReturnTypeSuccess,
			"bonjour-outer, monde-outer!",
		},
		{
			"outer envfile outer workdir",
			".env",
			`
DEFAULTS_GREETING=salutations-outer
DEFAULTS_MESSAGE_NAME=planete-outer
`,
			"",
			// The app dagger.json contains legacy workspace fields, so an
			// explicit outer-workdir invocation must select it as a workspace.
			// Direct `-m ./app` loading is covered by the legacy direct-load
			// error tests.
			[]string{"dagger", "-W", "./app", "call", "defaults", "message"},
			dagger.ReturnTypeSuccess,
			"salutations-outer, planete-outer!",
		},
	} {
		t.Run(tc.description, func(ctx context.Context, t *testctx.T) {
			stdout, err := ctr.
				WithNewFile(tc.dotEnvPath, tc.dotEnvContents).
				With(func(c *dagger.Container) *dagger.Container {
					if tc.workdir != "" {
						return c.WithWorkdir(tc.workdir)
					}
					return c
				}).
				WithExec(tc.command, dagger.ContainerWithExecOpts{
					Expect:                        tc.expect,
					ExperimentalPrivilegedNesting: true,
				}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.stdout, stdout)
		})
	}
}

// TestObjectDefaultOverride keeps coverage for .env user defaults overriding
// module defaults in compat mode. This regressed when a constructor arg's
// schema default (for example Python's "= None") was treated as explicit input.
func (WorkspaceCompatSuite) TestObjectDefaultOverride(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := moduleFixture(t, c, "python/object-default-override").
		WithEnvVariable("MY_SECRET", "hello-from-env")

	out, err := base.
		With(daggerCallAt(".", "check")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "secret is None", out)

	// User defaults currently match the constructor arg's GraphQL name
	// (`secretWithDefault`), not snake_case env-style names.
	out, err = base.
		WithNewFile(".env", "secretWithDefault=env://MY_SECRET").
		With(daggerCallAt(".", "check")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "secret is: hello-from-env", out)
}

func (WorkspaceCompatSuite) TestOuterEnvFile(ctx context.Context, t *testctx.T) {
	tmp := tempDirWithEnvFile(t,
		`DEFAULTS_GREETING=salutations`,
		`DEFAULTS_MESSAGE_NAME="tout le monde"`,
		`UNRELATED=yo`,
	)
	c := connect(ctx, t, dagger.WithWorkdir(tmp))
	src := c.Host().
		Directory(testModule(t, "go", "defaults")).
		AsModuleSource()
	t.Run("ModuleSource UserDefaults", func(ctx context.Context, t *testctx.T) {
		greeting, err := src.UserDefaults().Get(ctx, "GREETING")
		require.NoError(t, err)
		require.Equal(t, "salutations", greeting)
		messageName, err := src.UserDefaults().Get(ctx, "MESSAGE_NAME")
		require.NoError(t, err)
		require.Equal(t, "tout le monde", messageName)
		unrelated, err := src.UserDefaults().Get(ctx, "UNRELATED")
		require.NoError(t, err)
		require.Equal(t, "", unrelated)
	})

	t.Run("Module UserDefaults", func(ctx context.Context, t *testctx.T) {
		mod := src.AsModule()
		greeting, err := mod.UserDefaults().Get(ctx, "GREETING")
		require.NoError(t, err)
		require.Equal(t, "salutations", greeting)
		messageName, err := mod.UserDefaults().Get(ctx, "MESSAGE_NAME")
		require.NoError(t, err)
		require.Equal(t, "tout le monde", messageName)
		unrelated, err := mod.UserDefaults().Get(ctx, "UNRELATED")
		require.NoError(t, err)
		require.Equal(t, "", unrelated)
	})
}

func (WorkspaceCompatSuite) TestSystemVariables(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithWorkdir("defaults").
		WithNewFile(".env", `GREETING="${SYSTEM_GREETING}"`).
		WithEnvVariable("SYSTEM_GREETING", "live long and prosper").
		WithExec(daggerCallCmd(".", "message"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "live long and prosper, world!", output)
}

func (WorkspaceCompatSuite) TestRequiredDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithWorkdir("defaults").
		WithNewFile("/foo/dir/hello.txt", "").
		WithNewFile(".env", `LS_DIR=/foo/dir`).
		WithExec(daggerCallCmd(".", "ls"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err, "user default should successfully apply to required argument")
	require.Equal(t, "hello.txt\n", output, "user default should successfully apply to required argument")
}

func (WorkspaceCompatSuite) TestRequiredString(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile(".env", `DEFAULTS_CAPITALIZE_S=hello world`).
		WithWorkdir("defaults").
		WithExec(daggerCallCmd(".", "capitalize"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err, "user default should successfully apply to required argument")
	require.Equal(t, "HELLO WORLD", output, "user default should successfully apply to required argument")
}

func (WorkspaceCompatSuite) TestArgName(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := moduleFixture(t, c, "python/arg-name-defaults")

	t.Run("constructor and function args use GraphQL names", func(ctx context.Context, t *testctx.T) {
		ctr := base.WithNewFile(".env", `simpleValue=constructor-simple
httpUrl=constructor-url
ECHO_snakeCase=function-snake
ECHO_httpUrl=function-url
`)

		out, err := ctr.With(daggerCallAt(".", "constructor-values")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "constructor-simple|constructor-url", out)

		out, err = ctr.With(daggerCallAt(".", "echo")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "function-snake|function-url", out)
	})

	t.Run("source snake case names are not aliases", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			WithNewFile(".env", `simple_value=constructor-simple
http_url=constructor-url
`).
			WithExec(daggerCallCmd(".", "constructor-values"), dagger.ContainerWithExecOpts{
				Expect:                        dagger.ReturnTypeFailure,
				ExperimentalPrivilegedNesting: true,
			}).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "simpleValue")

		out, err = base.
			WithNewFile(".env", `simpleValue=constructor-simple
httpUrl=constructor-url
ECHO_snake_case=function-snake
ECHO_http_url=function-url
`).
			WithExec(daggerCallCmd(".", "echo"), dagger.ContainerWithExecOpts{
				Expect:                        dagger.ReturnTypeFailure,
				ExperimentalPrivilegedNesting: true,
			}).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "snake")
		require.Contains(t, out, "http")
	})
}

func (WorkspaceCompatSuite) TestDependencies(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile(".env", `FOOBAR_EXCLAIM_COUNT=4`).
		WithWorkdir("defaults").
		WithExec(daggerCallCmd(".", "message"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello, world!!!!", output, "User defaults should apply to nested dependencies")
}

func (WorkspaceCompatSuite) TestOptionalDirectoryWithIgnore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	docs := dag.Directory().
		WithNewFile("README.md", "Thank you for reading me. The end.").
		WithNewFile("Makefile", "lol")
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithDirectory("/foo/mydocs", docs).
		WithWorkdir("defaults").
		WithNewFile(".env", `docs=/foo/mydocs`).
		WithExec(daggerCallCmd(".", "docs", "entries"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "README.md\n", output)
}

func (WorkspaceCompatSuite) TestRequiredDirectoryWithIgnore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	docs := dag.Directory().
		WithNewFile("README.md", "Thank you for reading me. The end.").
		WithNewFile("Makefile", "lol")
	controlOutput, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithDirectory("/foo/mydocs", docs).
		WithWorkdir("defaults").
		WithExec(daggerCallCmd(".", "ls-text", "--dir=/foo/mydocs"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "README.md\n", controlOutput, "control - if this fails, something else is wrong")
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithDirectory("/foo/mydocs", docs).
		WithWorkdir("defaults").
		WithNewFile(".env", `lsText_dir=/foo/mydocs`).
		WithExec(daggerCallCmd(".", "ls-text"), nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "README.md\n", output)
}

// Test that it all works with a module that has a dash ("-") in its name
func (WorkspaceCompatSuite) TestModuleWithDash(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nestedDaggerContainer(t, c, "go", "defaults/super-dash-dash")
	outerEnv := c.EnvFile().
		WithVariable("SUPERDASHDASH_GREETING", "yay").
		WithVariable("SUPERDASHDASH_MESSAGE_NAME", "bob").
		AsFile()
	innerEnv := c.EnvFile().
		WithVariable("GREETING", "yay").
		WithVariable("MESSAGE_NAME", "bob").
		AsFile()
	for _, tc := range []struct {
		name    string
		ctr     *dagger.Container
		modPath string
	}{
		{
			"inner .env",
			base.WithWorkdir("defaults/super-dash-dash").WithFile(".env", innerEnv),
			".",
		},
		{
			"outer .env inner workdir",
			base.WithFile(".env", outerEnv).WithWorkdir("defaults/super-dash-dash"),
			".",
		},
		{
			"outer .env outer workdir",
			base.WithFile(".env", outerEnv).WithEnvVariable("DAGGER_MODULE", "./defaults/super-dash-dash"),
			"",
		},
		{
			"inner .env with prefix",
			// Use the outer env (which has prefix) as inner env. It should work.
			base.WithWorkdir("defaults/super-dash-dash").WithFile(".env", outerEnv),
			".",
		},
	} {
		tc := tc
		t.Run(tc.name+" introspect", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "--help"), nestedExec).
				Stdout(ctx)
			out = trimDaggerFunctionUsageText(out)
			require.NoError(t, err)
			require.Regexp(t, `(?m)--greeting string\s+\(default "yay"\)$`, out)
		})
		t.Run(tc.name+" call", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "message"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yay, bob!", out)
		})
	}
}

func (WorkspaceCompatSuite) TestConstructorOptional(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile("/foo/hello.txt", "hello there!").
		WithEnvVariable("PASSWORD", "topsecret")
	outerEnv := c.EnvFile().
		WithVariable("DEFAULTS_DIR", "/foo").
		WithVariable("DEFAULTS_FILE", "/foo/hello.txt").
		WithVariable("DEFAULTS_GREETING", "yay").
		WithVariable("DEFAULTS_password", "env://PASSWORD").
		AsFile()
	innerEnv := c.EnvFile().
		WithVariable("DIR", "/foo").
		WithVariable("FILE", "/foo/hello.txt").
		WithVariable("GREETING", "yay").
		WithVariable("password", "env://PASSWORD").
		AsFile()
	for _, tc := range []struct {
		name    string
		ctr     *dagger.Container
		modPath string
	}{
		{
			"inner .env",
			base.WithWorkdir("defaults").WithFile(".env", innerEnv),
			".",
		},
		{
			"outer .env inner workdir",
			base.WithFile(".env", outerEnv).WithWorkdir("defaults"),
			".",
		},
		{
			"outer .env outer workdir",
			base.WithFile(".env", outerEnv).WithEnvVariable("DAGGER_MODULE", "./defaults"),
			"",
		},
		{
			"inner .env with prefix",
			// Use the outer env (which has prefix) as inner env. It should work.
			base.WithWorkdir("defaults").WithFile(".env", outerEnv),
			".",
		},
	} {
		tc := tc
		t.Run(tc.name+" introspect", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "--help"), nestedExec).
				Stdout(ctx)
			out = trimDaggerFunctionUsageText(out)
			require.NoError(t, err)
			require.Regexp(t, `(?m)--greeting string\s+\(default "yay"\)$`, out)
			require.Regexp(t, `(?m)--password\s+Secret\s*$`, out)
			require.Regexp(t, `(?m)--dir\s+Directory\s*$`, out)
			require.Regexp(t, `(?m)--file\s+File\s*$`, out)
		})
		t.Run(tc.name+" call", func(ctx context.Context, t *testctx.T) {
			// Test that 'greeting' is used
			out, err := tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "message"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yay, world!", out)
			// Test that 'file' is used
			out, err = tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "file", "contents"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello there!", out)
			// Test that 'dir' is used
			out, err = tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "dir", "entries"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello.txt\n", out)
			// Test that 'password' is used
			out, err = tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "password", "plaintext"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "topsecret", out)
		})
	}
}

func (WorkspaceCompatSuite) TestConstructorOptionalEmptySecret(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	out, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithEnvVariable("PASSWORD", "").
		WithWorkdir("defaults").
		WithNewFile(".env", "password=env://PASSWORD").
		WithExec([]string{"dagger", "call", "-m", ".", "password", "plaintext"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "", out)
}

func (WorkspaceCompatSuite) TestConstructorPlaintextSecretDefault(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := daggerCliBase(t, c).
		WithNewFile("dagger.json", `{"name":"test","engineVersion":"latest","sdk":{"source":"go"},"source":"."}`).
		WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

func New(
	password *dagger.Secret,
	somekey string,
) *Test {
	return &Test{}
}

type Test struct{}
`).
		WithNewFile(".env", "password=topsecret\nsomekey=somevalue\n")

	out, err := ctr.
		WithExec([]string{"dagger", "call", "-m", ".", "--help"}, nestedExec).
		Stderr(ctx)
	require.NoError(t, err)
	require.NotContains(t, out, "topsecret")
	require.Contains(t, out, `user default: test(password=*****)`)
	require.Contains(t, out, `user default: test(somekey="somevalue")`)
}

func (WorkspaceCompatSuite) TestConstructorRequired(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nestedDaggerContainer(t, c, "go", "defaults/superconstructor").
		WithNewFile("/foo/hello.txt", "hello there!").
		WithEnvVariable("PASSWORD", "topsecret").
		WithServiceBinding("www", c.Container().From("nginx").AsService())
	outerEnv := c.EnvFile().
		WithVariable("SUPERCONSTRUCTOR_DIR", "/foo").
		WithVariable("SUPERCONSTRUCTOR_FILE", "/foo/hello.txt").
		WithVariable("SUPERCONSTRUCTOR_COUNT", "42").
		WithVariable("SUPERCONSTRUCTOR_greeting", "yay").
		WithVariable("SUPERCONSTRUCTOR_password", "env://PASSWORD").
		WithVariable("SUPERCONSTRUCTOR_service", "tcp://www:80").
		AsFile()
	innerEnv := c.EnvFile().
		WithVariable("DIR", "/foo").
		WithVariable("FILE", "/foo/hello.txt").
		WithVariable("COUNT", "42").
		WithVariable("greeting", "yay").
		WithVariable("password", "env://PASSWORD").
		WithVariable("service", "tcp://www:80").
		AsFile()
	for _, tc := range []struct {
		name    string
		ctr     *dagger.Container
		modPath string
	}{
		{
			"inner .env",
			base.WithWorkdir("defaults/superconstructor").WithFile(".env", innerEnv),
			".",
		},
		{
			"outer .env inner workdir",
			base.WithFile(".env", outerEnv).WithWorkdir("defaults/superconstructor"),
			".",
		},
		{
			"outer .env outer workdir",
			base.WithFile(".env", outerEnv).WithEnvVariable("DAGGER_MODULE", "./defaults/superconstructor"),
			"",
		},
	} {
		tc := tc
		t.Run(tc.name+" introspect", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "--help"), nestedExec).
				Stdout(ctx)
			out = trimDaggerFunctionUsageText(out)
			require.NoError(t, err)
			require.Regexp(t, `(?m)--count int *\(default 42\)\s*$`, out)
			require.Regexp(t, `(?m)--greeting string *\(default "yay"\)\s*$`, out)
			// Using (?m) multiline mode to match --service Service at end of line
			// testify's require.Regexp supports Go's regexp package which includes (?m) flag
			require.Regexp(t, `(?m)--service\s*Service\s*$`, out)
			require.Regexp(t, `(?m)--password\s*Secret\s*$`, out)
			require.Regexp(t, `(?m)--dir\s*Directory\s*$`, out)
			require.Regexp(t, `(?m)--file\s*File\s*$`, out)
		})
		t.Run(tc.name+" call", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "greeting"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yay", out, "user default should be applied")
			out, err = tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "count"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "42", out, "user default should be applied")
			out, err = tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "file", "contents"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello there!", out, "user default should be applied")
			out, err = tc.ctr.
				WithExec(daggerCallCmd(tc.modPath, "dir", "entries"), nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello.txt\n", out, "user default should be applied")
		})
	}
}

func (WorkspaceCompatSuite) TestCompatEnvPrefixLookupPolicy(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name        string
		module      string
		envPath     string
		envContents string
		workdir     string
		command     []string
		stdout      string
	}{
		{
			name:    "inner env uses unprefixed keys",
			module:  "defaults",
			envPath: "defaults/.env",
			envContents: `GREETING=inner
MESSAGE_NAME=inner-name
`,
			workdir: "defaults",
			command: daggerCallCmd(".", "message"),
			stdout:  "inner, inner-name!",
		},
		{
			name:    "outer env uses module prefix",
			module:  "defaults",
			envPath: ".env",
			envContents: `DEFAULTS_GREETING=outer
DEFAULTS_MESSAGE_NAME=outer-name
`,
			command: []string{"dagger", "-m", "./defaults", "call", "message"},
			stdout:  "outer, outer-name!",
		},
		{
			name:    "inner env may use module prefix",
			module:  "defaults",
			envPath: "defaults/.env",
			envContents: `DEFAULTS_GREETING=inner-prefixed
DEFAULTS_MESSAGE_NAME=inner-prefixed-name
`,
			workdir: "defaults",
			command: daggerCallCmd(".", "message"),
			stdout:  "inner-prefixed, inner-prefixed-name!",
		},
		{
			name:    "dashed module accepts compact normalized prefix",
			module:  "defaults/super-dash-dash",
			envPath: ".env",
			envContents: `SUPERDASHDASH_GREETING=compact
SUPERDASHDASH_MESSAGE_NAME=compact-name
`,
			command: []string{"dagger", "-m", "./defaults/super-dash-dash", "call", "message"},
			stdout:  "compact, compact-name!",
		},
		{
			name:    "dashed module accepts original-name snake prefix",
			module:  "defaults/super-dash-dash",
			envPath: ".env",
			envContents: `SUPER_DASH_DASH_GREETING=snake
SUPER_DASH_DASH_MESSAGE_NAME=snake-name
`,
			command: []string{"dagger", "-m", "./defaults/super-dash-dash", "call", "message"},
			stdout:  "snake, snake-name!",
		},
		{
			name:    "dashed module accepts original-name lower camel prefix",
			module:  "defaults/super-dash-dash",
			envPath: ".env",
			envContents: `superDashDash_GREETING=camel
superDashDash_MESSAGE_NAME=camel-name
`,
			command: []string{"dagger", "-m", "./defaults/super-dash-dash", "call", "message"},
			stdout:  "camel, camel-name!",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			ctr := nestedDaggerContainer(t, c, "go", tc.module).
				WithNewFile(tc.envPath, tc.envContents)
			if tc.workdir != "" {
				ctr = ctr.WithWorkdir(tc.workdir)
			}

			stdout, err := ctr.WithExec(tc.command, nestedExec).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.stdout, stdout)
		})
	}
}

func (WorkspaceCompatSuite) TestCaching(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := nestedDaggerContainer(t, c, "go", "defaults")
	// First run
	output1, err := ctr.
		WithNewFile(`.env`, `DEFAULTS_GREETING=greeting1`).
		WithExec([]string{"dagger", "-m", "./defaults", "call", "message"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "greeting1, world!", output1)
	// Second run. Only the .env changes
	output2, err := ctr.
		WithNewFile(`.env`, `DEFAULTS_GREETING=greeting2`).
		WithExec([]string{"dagger", "-m", "./defaults", "call", "message"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	// The two outputs MUST BE DIFFERENT
	// If they are the same, it means the second run had a false positive cache hit
	require.Equal(t, "greeting2, world!", output2, "same module source with different env file, should not be cached")
}

func (WorkspaceCompatSuite) TestSimple(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nestedDaggerContainer(t, c, "go", "defaults")
	for _, tc := range []struct {
		description    string
		dotEnvPath     string
		dotEnvContents string
		workdir        string
		command        []string
		expect         dagger.ReturnType
		stdout         string
		prepare        func(ctr *dagger.Container) *dagger.Container
	}{
		{
			"inner envfile",
			"./defaults/.env",
			`
GREETING=salut
MESSAGE_NAME=monde
			`,
			"./defaults",
			daggerCallCmd(".", "message"),
			dagger.ReturnTypeSuccess,
			"salut, monde!",
			nil,
		},
		{
			"inner envfile list",
			"./defaults/.env",
			`
LIST=1,2,3
			`,
			"./defaults",
			daggerCallCmd(".", "list-string"),
			dagger.ReturnTypeSuccess,
			"1\n2\n3\n",
			nil,
		},
		{
			"inner envfile secret list",
			"./defaults/.env",
			`
SECRETS=env://FOO,env://BAR,env://BAZ
			`,
			"./defaults",
			daggerCallCmd(".", "list-secrets"),
			dagger.ReturnTypeSuccess,
			"1\n2\n3\n",
			func(c *dagger.Container) *dagger.Container {
				c = c.WithEnvVariable("FOO", "1").
					WithEnvVariable("BAR", "2").
					WithEnvVariable("BAZ", "3")
				return c
			},
		},
		{
			"inner string with commas",
			"./defaults/.env",
			`
GREETING="one,two"
			`,
			"./defaults",
			daggerCallCmd(".", "message"),
			dagger.ReturnTypeSuccess,
			"one,two, world!",
			nil,
		},
		{
			"outer envfile inner workdir",
			".env",
			`
DEFAULTS_GREETING=bonjour
DEFAULTS_MESSAGE_NAME=monde
			`,
			"./defaults",
			daggerCallCmd(".", "message"),
			dagger.ReturnTypeSuccess,
			"bonjour, monde!",
			nil,
		},
		{
			"outer envfile outer workdir",
			".env",
			`
DEFAULTS_GREETING=bonjour
DEFAULTS_MESSAGE_NAME=monde
`,
			"",
			[]string{"dagger", "-m", "./defaults", "call", "message"},
			dagger.ReturnTypeSuccess,
			"bonjour, monde!",
			nil,
		},
		{
			"preserve quotes",
			".env",
			`
DEFAULTS_GREETING='{"foo":"bar"}'
`,
			"",
			[]string{"dagger", "-m", "./defaults", "call", "greeting"},
			dagger.ReturnTypeSuccess,
			`{"foo":"bar"}`,
			nil,
		},
		{
			"preserve quotes without namespace",
			"./defaults/.env",
			`
GREETING='{"hello":"world"}'
`,
			"defaults",
			[]string{"dagger", "call", "greeting"},
			dagger.ReturnTypeSuccess,
			`{"hello":"world"}`,
			nil,
		},
	} {
		t.Run(tc.description, func(ctx context.Context, t *testctx.T) {
			ctr := base
			if tc.prepare != nil {
				ctr = tc.prepare(ctr)
			}
			stdout, err := ctr.
				WithNewFile(tc.dotEnvPath, tc.dotEnvContents).
				With(func(c *dagger.Container) *dagger.Container {
					if tc.workdir != "" {
						return c.WithWorkdir(tc.workdir)
					}
					return c
				}).
				WithExec(tc.command, dagger.ContainerWithExecOpts{
					Expect:                        tc.expect,
					ExperimentalPrivilegedNesting: true,
				}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.stdout, stdout, tc.description)
		})
	}
}
