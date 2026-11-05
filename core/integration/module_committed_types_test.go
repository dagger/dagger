package core

// These tests cover how the engine learns the API of a Go module that commits
// its bindings: it reads the registration code in dagger.gen.go instead of
// building the module and running it, and builds when that code doesn't match
// the module's source or isn't what it can follow. What a listing shows must be
// an API the module has.

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/core/modules"
)

type CommittedTypesSuite struct{}

func TestCommittedTypes(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CommittedTypesSuite{})
}

const committedTypesGenerate = `{moduleSource(refString:"."){generatedContextDirectory{export(path:".")}}}`

// committedTypesModule is a module with its bindings generated and committed.
func committedTypesModule(t testing.TB, c *dagger.Client, name, engineVersion, source string) *core.Container {
	t.Helper()
	return goGitBase(t, c).
		WithNewFile("main.go", source).
		With(configFile(".", &modules.ModuleConfig{
			Name:          name,
			EngineVersion: engineVersion,
			SDK:           &modules.SDK{Source: "go"},
		})).
		With(daggerQuery(committedTypesGenerate))
}

const committedTypesSource = `package main

import "context"

type Status string

const (
	StatusReady Status = "ready"
	StatusDone  Status = "done"
)

type Hello struct {
	Greeting string
	// +private
	Secret string
}

type Item struct {
	Name string
}

func New(
	// +default="hello"
	greeting string,
) *Hello {
	return &Hello{Greeting: greeting}
}

func (m *Hello) Say(ctx context.Context, name string) (string, error) {
	return m.Greeting + " " + name, nil
}

func (m *Hello) Item() *Item {
	return &Item{Name: "item"}
}

func (m *Hello) EchoStatus(status Status) Status {
	return status
}

func (m *Hello) Value() string {
	return "value"
}
`

// A broken function body proves the listing built nothing, and the compile
// error reaches the first call.
func (CommittedTypesSuite) TestBrokenBodyFailsAtFirstCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := committedTypesModule(t, c, "hello", modules.EngineVersionLatest, committedTypesSource).
		WithNewFile("broken.go", `package main

func broken() string {
	return undefinedSymbol()
}
`)

	out, err := mod.With(daggerFunctions("-m", ".")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "say")

	_, err = mod.With(daggerCallAt(".", "say", "--name", "x")).Sync(ctx)
	requireErrOut(t, err, "undefined: undefinedSymbol")
}

// A module that compiles and panics as it starts fails at its first call too.
func (CommittedTypesSuite) TestInitPanicFailsAtFirstCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := committedTypesModule(t, c, "hello", modules.EngineVersionLatest, committedTypesSource).
		WithNewFile("init.go", `package main

func init() {
	panic("failed to start")
}
`)

	out, err := mod.With(daggerFunctions("-m", ".")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "say")

	_, err = mod.With(daggerCallAt(".", "say", "--name", "x")).Sync(ctx)
	requireErrOut(t, err, "failed to start")
}

// A stale module that still compiles lists what its binary has: the new method
// isn't listed, and isn't callable.
func (CommittedTypesSuite) TestStaleBindingsThatCompile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := committedTypesModule(t, c, "hello", modules.EngineVersionLatest, committedTypesSource).
		WithNewFile("added.go", `package main

func (m *Hello) Added() string {
	return "added"
}
`)

	out, err := mod.With(daggerFunctions("-m", ".")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "say")
	require.NotContains(t, out, "added")

	out, err = mod.With(daggerCallAt(".", "say", "--name", "x")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello x")

	_, err = mod.With(daggerCallAt(".", "added")).Sync(ctx)
	requireErrOut(t, err, `unknown command "added"`)
}

// Bindings that mention what the source no longer declares would list an API
// the module doesn't have. Such a module builds, and its listing fails with the
// compile error.
func (CommittedTypesSuite) TestStaleBindingsThatDoNotCompile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := committedTypesModule(t, c, "hello", modules.EngineVersionLatest, committedTypesSource)

	for _, tc := range []struct {
		name     string
		old, new string
		want     string
	}{
		{
			"function removed",
			"func (m *Hello) Value() string {\n\treturn \"value\"\n}\n", "",
			"Value undefined",
		},
		{
			"object removed",
			"type Item struct {\n\tName string\n}\n", "type gone struct{}\n",
			"undefined: Item",
		},
		{
			"field removed",
			"\t// +private\n\tSecret string\n", "",
			"Secret undefined",
		},
		{
			"enum member removed",
			"\tStatusDone  Status = \"done\"\n", "",
			"undefined: StatusDone",
		},
		{
			"argument removed",
			"ctx context.Context, name string", "ctx context.Context",
			"too many arguments",
		},
		{
			"argument added",
			"ctx context.Context, name string", "ctx context.Context, name string, extra bool",
			"not enough arguments",
		},
		{
			"argument type changed",
			"ctx context.Context, name string", "ctx context.Context, name int",
			"cannot use",
		},
		{
			"return type changed",
			"func (m *Hello) EchoStatus(status Status) Status {\n\treturn status\n}\n",
			"func (m *Hello) EchoStatus(status Status) (Status, string) {\n\treturn status, \"\"\n}\n",
			"EchoStatus",
		},
		{
			"a method the generated code declares too",
			"func (m *Hello) Value() string {", "func (h Hello) MarshalJSON() ([]byte, error) {\n\treturn nil, nil\n}\n\nfunc (m *Hello) Value() string {",
			"MarshalJSON",
		},
		{
			"enum member type changed",
			"\tStatusDone  Status = \"done\"\n", "\tStatusDone = 1\n",
			"StatusDone",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			require.Contains(t, committedTypesSource, tc.old)
			source := strings.Replace(committedTypesSource, tc.old, tc.new, 1)
			if tc.name == "object removed" {
				source = strings.Replace(source, "func (m *Hello) Item() *Item {\n\treturn &Item{Name: \"item\"}\n}\n", "", 1)
			}
			if tc.name == "argument removed" {
				source = strings.Replace(source, `m.Greeting + " " + name`, "m.Greeting", 1)
			}
			if tc.name == "argument type changed" {
				source = strings.Replace(source, `m.Greeting + " " + name`, "m.Greeting", 1)
			}
			_, err := mod.WithNewFile("main.go", source).With(daggerFunctions("-m", ".")).Sync(ctx)
			requireErrOut(t, err, tc.want)
		})
	}
}

// Registration code the reader can't follow builds, and the module has the same
// API either way.
func (CommittedTypesSuite) TestMatchesTheBuiltDefinition(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	read := goGitBase(t, c).
		With(withModuleFixture(t, c, ".", "go/committed-types")).
		With(configFile(".", &modules.ModuleConfig{
			Name:          "feature",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "go"},
		})).
		With(daggerQuery(committedTypesGenerate))

	generated, err := read.File("dagger.gen.go").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, generated, "return dag.Module().")
	built := read.WithNewFile("dagger.gen.go", strings.Replace(generated,
		"return dag.Module().",
		"return dag.Module().With(func(m *dagger.Module) *dagger.Module { return m }).",
		1))

	// A broken body doesn't stop the listing only if the fixture is being read,
	// so the comparison below isn't of two builds.
	out, err := read.
		WithNewFile("broken.go", "package main\n\nfunc broken() string {\n\treturn undefinedSymbol()\n}\n").
		With(daggerFunctions("-m", ".")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello")

	const query = `{moduleSource(refString:"."){clientSchemaIntrospectionJSON{contents} asModule{description dependencies{name}}}}`
	readOut, err := read.With(daggerQuery(query)).Stdout(ctx)
	require.NoError(t, err)
	builtOut, err := built.With(daggerQuery(query)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, builtOut, readOut)
}

// Functions that reach the module's own source go through codegen with its
// dependencies, where the module mustn't be one of them.
func (CommittedTypesSuite) TestModuleReadingItself(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := goGitBase(t, c).
		With(withModuleFixture(t, c, ".", "go/committed-types")).
		With(configFile(".", &modules.ModuleConfig{
			Name:          "feature",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "go"},
		})).
		With(daggerQuery(committedTypesGenerate))

	out, err := mod.With(daggerCallAt(".", "read-own-source")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `name = "feature"`)

	out, err = mod.With(daggerCallAt(".", "read-own-generated")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "dagger.gen.go")

	out, err = mod.With(daggerCallAt(".", "self-call")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello self")

	out, err = mod.With(daggerQuery(`{moduleSource(refString:"."){asModule{dependencies{name}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.NotContains(t, out, `"feature"`)
}

// Before v0.18.11 an enum member's name is its value: the compiled module's
// calls mean that in its own API view, and so must the reader's.
func (CommittedTypesSuite) TestReadsInTheModulesAPIView(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := committedTypesModule(t, c, "hello", "v0.18.10", committedTypesSource)

	generated, err := mod.File("dagger.gen.go").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, generated, `WithEnumMember("Ready"`)

	out, err := mod.With(daggerQuery(`{moduleSource(refString:"."){asModule{enums{asEnum{members{name value}}}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `"name": "ready"`)
	require.NotContains(t, out, `"name": "READY"`)

	out, err = mod.With(daggerCallAt(".", "echo-status", "--status", "ready")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "ready")
}

// Removing the generated bindings package is the error that says to generate.
func (CommittedTypesSuite) TestMissingBindingsPackage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	_, err := committedTypesModule(t, c, "hello", modules.EngineVersionLatest, committedTypesSource).
		WithoutFile("internal/dagger/dagger.gen.go").
		With(daggerFunctions("-m", ".")).
		Sync(ctx)
	requireErrOut(t, err, "generated file")
	requireErrOut(t, err, "run `dagger generate`")
}

// A module with bindings generated at load has no committed code to read.
func (CommittedTypesSuite) TestRuntimeCodegenModulesStillBuild(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	_, err := goGitBase(t, c).
		WithNewFile("main.go", `package main

type Hello struct{}

func (m *Hello) Say() string {
	return undefinedSymbol()
}
`).
		With(fileContents("dagger.json", `{"name": "hello", "engineVersion": "latest", "sdk": {"source": "go"}}`)).
		With(daggerFunctions("-m", ".")).
		Sync(ctx)
	requireErrOut(t, err, "undefined: undefinedSymbol")
}

// The engine knows not to read a module whose bindings are generated at load,
// so nothing fails on its way to the runtime. The progress a failed query
// prints shows every step that failed.
func (CommittedTypesSuite) TestRuntimeCodegenModulesShowNoFailedStep(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	stderr, err := goGitBase(t, c).
		WithNewFile("main.go", `package main

type Hello struct{}

func (m *Hello) Say() string {
	return "hello"
}
`).
		With(fileContents("dagger.json", `{"name": "hello", "engineVersion": "latest", "sdk": {"source": "go"}}`)).
		WithExec([]string{"dagger", "query", "-m", "."}, core.ContainerWithExecOpts{
			UseEntrypoint: true,
			Stdin:         `{noSuchField}`,
			Expect:        core.ReturnTypeFailure,
		}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "noSuchField")
	require.NotContains(t, stderr, "module types need")
}
