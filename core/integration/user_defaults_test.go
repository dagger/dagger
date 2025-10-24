package core

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type UserDefaultsSuite struct{}

func TestUserDefaults(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(UserDefaultsSuite{})
}

func (UserDefaultsSuite) TestRemoteFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithWorkdir("defaults").
		WithNewFile(".env", `DEFAULTS_FILE=https://github.com/dagger/dagger#main:cmd/dagger/main.go`).
		WithExec([]string{"dagger", "call", "file", "contents"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, output, "package main")
}

func (UserDefaultsSuite) TestLocalFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile("hello.txt", "well hello!").
		WithWorkdir("defaults").
		WithNewFile(".env", `DEFAULTS_FILE=../hello.txt`).
		WithExec([]string{"dagger", "call", "file", "contents"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "well hello!", output)
}

func (UserDefaultsSuite) TestLocalDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithDirectory("data", dag.Directory().WithNewFile("hello.txt", "well hello!")).
		WithWorkdir("defaults").
		WithNewFile(".env", `DEFAULTS_DIR=../data`).
		WithExec([]string{"dagger", "call", "dir", "file", "--path=hello.txt", "contents"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "well hello!", output)
}

func (UserDefaultsSuite) TestRemoteDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithWorkdir("defaults").
		WithNewFile(".env", `DIR=https://github.com/dagger/dagger#main:cmd/dagger`).
		WithExec([]string{"dagger", "call", "dir", "file", "--path=main.go", "contents"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, output, "package main")
}

func (UserDefaultsSuite) TestLocalBlueprint(ctx context.Context, t *testctx.T) {
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
			[]string{"dagger", "-m", "./app", "call", "message"},
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

func (UserDefaultsSuite) TestOuterEnvFile(ctx context.Context, t *testctx.T) {
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

func (UserDefaultsSuite) TestSystemVariables(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithWorkdir("defaults").
		WithNewFile(".env", `GREETING="${SYSTEM_GREETING}"`).
		WithEnvVariable("SYSTEM_GREETING", "live long and prosper").
		WithExec([]string{"dagger", "call", "message"}, nestedExec).
		Stdout(ctx)
	// FIXME System env variable lookup is temporarily disabled
	// see https://github.com/dagger/dagger/pull/11034#discussion_r2401382370
	require.Error(t, err)
	require.NotEqual(t, "live long and prosper, world!", output, "output should NOT include the system env variable (feature temporarily disabled)")
}

func (UserDefaultsSuite) TestRequiredDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithWorkdir("defaults").
		WithNewFile("/foo/dir/hello.txt", "").
		WithNewFile(".env", `LS_DIR=/foo/dir`).
		WithExec([]string{"dagger", "call", "ls"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err, "user default should successfully apply to required argument")
	require.Equal(t, "hello.txt\n", output, "user default should successfully apply to required argument")
}

func (UserDefaultsSuite) TestRequiredString(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile(".env", `DEFAULTS_CAPITALIZE_S=hello world`).
		WithWorkdir("defaults").
		WithExec([]string{"dagger", "call", "capitalize"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err, "user default should successfully apply to required argument")
	require.Equal(t, "HELLO WORLD", output, "user default should successfully apply to required argument")
}

func (UserDefaultsSuite) TestArgName(ctx context.Context, t *testctx.T) {
	t.Skip(`FIXME: test conversion between arg name and env var name (eg."FOO_BAR=hello" -> "fooBar=hello"`)
}

func (UserDefaultsSuite) TestDependencies(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithNewFile(".env", `FOOBAR_EXCLAIM_COUNT=4`).
		WithWorkdir("defaults").
		WithExec([]string{"dagger", "call", "message"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello, world!!!!", output, "User defaults should apply to nested dependencies")
}

func (UserDefaultsSuite) TestOptionalDirectoryWithIgnore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	docs := dag.Directory().
		WithNewFile("README.md", "Thank you for reading me. The end.").
		WithNewFile("Makefile", "lol")
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithDirectory("/foo/mydocs", docs).
		WithWorkdir("defaults").
		WithNewFile(".env", `docs=/foo/mydocs`).
		WithExec([]string{"dagger", "call", "docs", "entries"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "README.md\n", output)
}

func (UserDefaultsSuite) TestRequiredDirectoryWithIgnore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	docs := dag.Directory().
		WithNewFile("README.md", "Thank you for reading me. The end.").
		WithNewFile("Makefile", "lol")
	controlOutput, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithDirectory("/foo/mydocs", docs).
		WithWorkdir("defaults").
		WithExec([]string{"dagger", "call", "ls-text", "--dir=/foo/mydocs"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "README.md\n", controlOutput, "control - if this fails, something else is wrong")
	output, err := nestedDaggerContainer(t, c, "go", "defaults").
		WithDirectory("/foo/mydocs", docs).
		WithWorkdir("defaults").
		WithNewFile(".env", `lsText_dir=/foo/mydocs`).
		WithExec([]string{"dagger", "call", "ls-text"}, nestedExec).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "README.md\n", output)
}

var nestedExec = dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true}

// Test that it all works with a module that has a dash ("-") in its name
func (UserDefaultsSuite) TestModuleWithDash(ctx context.Context, t *testctx.T) {
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
		name string
		ctr  *dagger.Container
	}{
		{
			"inner .env",
			base.WithWorkdir("defaults/super-dash-dash").WithFile(".env", innerEnv),
		},
		{
			"outer .env inner workdir",
			base.WithFile(".env", outerEnv).WithWorkdir("defaults/super-dash-dash"),
		},
		{
			"outer .env outer workdir",
			base.WithFile(".env", outerEnv).WithEnvVariable("DAGGER_MODULE", "./defaults/super-dash-dash"),
		},
		{
			"inner .env with prefix",
			// Use the outer env (which has prefix) as inner env. It should work.
			base.WithWorkdir("defaults/super-dash-dash").WithFile(".env", outerEnv),
		},
	} {
		tc := tc
		t.Run(tc.name+" introspect", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec([]string{"dagger", "call", "--help"}, nestedExec).
				Stdout(ctx)
			out = trimDaggerFunctionUsageText(out)
			require.NoError(t, err)
			require.Regexp(t, `(?m)--greeting string\s+\(default "yay"\)$`, out)
		})
		t.Run(tc.name+" call", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec([]string{"dagger", "call", "message"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yay, bob!", out)
		})
	}
}

func (UserDefaultsSuite) TestConstructorOptional(ctx context.Context, t *testctx.T) {
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
		name string
		ctr  *dagger.Container
	}{
		{
			"inner .env",
			base.WithWorkdir("defaults").WithFile(".env", innerEnv),
		},
		{
			"outer .env inner workdir",
			base.WithFile(".env", outerEnv).WithWorkdir("defaults"),
		},
		{
			"outer .env outer workdir",
			base.WithFile(".env", outerEnv).WithEnvVariable("DAGGER_MODULE", "./defaults"),
		},
		{
			"inner .env with prefix",
			// Use the outer env (which has prefix) as inner env. It should work.
			base.WithWorkdir("defaults").WithFile(".env", outerEnv),
		},
	} {
		tc := tc
		t.Run(tc.name+" introspect", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec([]string{"dagger", "call", "--help"}, nestedExec).
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
				WithExec([]string{"dagger", "call", "message"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yay, world!", out)
			// Test that 'file' is used
			out, err = tc.ctr.
				WithExec([]string{"dagger", "call", "file", "contents"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello there!", out)
			// Test that 'dir' is used
			out, err = tc.ctr.
				WithExec([]string{"dagger", "call", "dir", "entries"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello.txt\n", out)
			// Test that 'password' is used
			out, err = tc.ctr.
				WithExec([]string{"dagger", "call", "password", "plaintext"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "topsecret", out)
		})
	}
}

func trimDaggerFunctionUsageText(s string) string {
	// Trim the output for readability
	start := strings.Index(s, "ARGUMENTS")
	end := strings.Index(s, "OPTIONS")
	if start >= 0 && end > start {
		s = s[start:end]
	}
	return s
}

func (UserDefaultsSuite) TestConstructorRequired(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := nestedDaggerContainer(t, c, "go", "defaults/superconstructor").
		WithNewFile("/foo/hello.txt", "hello there!").
		WithEnvVariable("PASSWORD", "topsecret").
		WithServiceBinding("www", c.Container().From("nginx").AsService())
	// FIXME: call lookupPrefix()
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
		name string
		ctr  *dagger.Container
	}{
		{
			"inner .env",
			base.WithWorkdir("defaults/superconstructor").WithFile(".env", innerEnv),
		},
		{
			"outer .env inner workdir",
			base.WithFile(".env", outerEnv).WithWorkdir("defaults/superconstructor"),
		},
		{
			"outer .env outer workdir",
			base.WithFile(".env", outerEnv).WithEnvVariable("DAGGER_MODULE", "./defaults/superconstructor"),
		},
	} {
		tc := tc
		t.Run(tc.name+" introspect", func(ctx context.Context, t *testctx.T) {
			out, err := tc.ctr.
				WithExec([]string{"dagger", "call", "--help"}, nestedExec).
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
				WithExec([]string{"dagger", "call", "greeting"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "yay", out, "user default should be applied")
			out, err = tc.ctr.
				WithExec([]string{"dagger", "call", "count"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "42", out, "user default should be applied")
			out, err = tc.ctr.
				WithExec([]string{"dagger", "call", "file", "contents"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello there!", out, "user default should be applied")
			out, err = tc.ctr.
				WithExec([]string{"dagger", "call", "dir", "entries"}, nestedExec).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello.txt\n", out, "user default should be applied")
		})
	}
}

func (UserDefaultsSuite) TestCaching(ctx context.Context, t *testctx.T) {
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

func nestedDaggerContainer(t *testctx.T, c *dagger.Client, modLang, modName string) *dagger.Container {
	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/work").
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))
	if modLang != "" && modName != "" {
		ctr = ctr.
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithDirectory(modName, c.Host().Directory(testModule(t, modLang, modName)))
	}
	return ctr
}

func testModule(t *testctx.T, lang, name string) string {
	modulePath, err := filepath.Abs(path.Join("testdata", "modules", lang, name))
	require.NoError(t, err)
	return modulePath
}

func tempDirWithEnvFile(t *testctx.T, environ ...string) string {
	tmp := t.TempDir()
	os.WriteFile(tmp+"/.env", []byte(strings.Join(environ, "\n")), 0600)
	return tmp
}

func (UserDefaultsSuite) TestSimple(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := nestedDaggerContainer(t, c, "go", "defaults")
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
			"./defaults/.env",
			`
GREETING=salut
MESSAGE_NAME=monde
`,
			"./defaults",
			[]string{"dagger", "call", "message"},
			dagger.ReturnTypeSuccess,
			"salut, monde!",
		},
		{
			"outer envfile inner workdir",
			".env",
			`
DEFAULTS_GREETING=bonjour
DEFAULTS_MESSAGE_NAME=monde
`,
			"./defaults",
			[]string{"dagger", "call", "message"},
			dagger.ReturnTypeSuccess,
			"bonjour, monde!",
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
		},
	} {
		tc := tc
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
			require.Equal(t, tc.stdout, stdout, tc.description)
		})
	}
}
