package core

// These tests cover modules authored with the Go SDK. They verify generated Go
// bindings and executing Go module functions.
//
// See also:
// - module_definition_test.go: SDK-neutral module API definition behavior.
// - module_type_test.go: cross-SDK custom type behavior.

import (
	"context"
	"fmt"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type GoSuite struct{}

func TestGo(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(GoSuite{})
}

func (GoSuite) TestSignatures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/minimal")

	t.Run("func Hello() string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{hello}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hello":"hello"}`, out)
	})

	t.Run("func Echo(string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echo(msg: "hello")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echo":"hello...hello...hello..."}`, out)
	})

	t.Run("func EchoPointer(*string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoPointer(msg: "hello")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoPointer":"hello...hello...hello..."}`, out)
	})

	t.Run("func EchoPointerPointer(**string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoPointerPointer(msg: "hello")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoPointerPointer":"hello...hello...hello..."}`, out)
	})

	t.Run("func EchoOptional(string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOptional(msg: "hello")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptional":"hello...hello...hello..."}`, out)
		out, err = modGen.With(daggerQueryAt(".", `{echoOptional}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptional":"default...default...default..."}`, out)
	})

	t.Run("func EchoOptionalPointer(string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOptionalPointer(msg: "hello")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptionalPointer":"hello...hello...hello..."}`, out)
		out, err = modGen.With(daggerQueryAt(".", `{echoOptionalPointer}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptionalPointer":"default...default...default..."}`, out)
	})

	t.Run("func EchoOptionalSlice([]string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOptionalSlice(msg: ["hello", "there"])}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptionalSlice":"hello+there...hello+there...hello+there..."}`, out)
		out, err = modGen.With(daggerQueryAt(".", `{echoOptionalSlice}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptionalSlice":"foobar...foobar...foobar..."}`, out)
	})

	t.Run("func Echoes([]string) []string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoes(msgs: ["hello"])}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoes":["hello...hello...hello..."]}`, out)
	})

	t.Run("func EchoesVariadic(...string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoesVariadic(msgs: ["hello"])}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoesVariadic":"hello...hello...hello..."}`, out)
	})

	t.Run("func HelloContext(context.Context) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{helloContext}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"helloContext":"hello context"}`, out)
	})

	t.Run("func EchoContext(context.Context, string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoContext(msg: "hello")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoContext":"ctx.hello...ctx.hello...ctx.hello..."}`, out)
	})

	t.Run("func HelloStringError() (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{helloStringError}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"helloStringError":"hello i worked"}`, out)
	})

	t.Run("func HelloVoid()", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{helloVoid}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"helloVoid":null}`, out)
	})

	t.Run("func HelloVoidError() error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{helloVoidError}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"helloVoidError":null}`, out)
	})

	t.Run("func EchoOpts(string, string, int) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOpts(msg: "hi")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOpts":"hi"}`, out)

		out, err = modGen.With(daggerQueryAt(".", `{echoOpts(msg: "hi", suffix: "!", times: 2)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOpts":"hi!hi!"}`, out)
	})

	t.Run("func EchoOptsInline(struct{string, string, int}) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOptsInline(msg: "hi")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsInline":"hi"}`, out)

		out, err = modGen.With(daggerQueryAt(".", `{echoOptsInline(msg: "hi", suffix: "!", times: 2)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsInline":"hi!hi!"}`, out)
	})

	t.Run("func EchoOptsInlinePointer(*struct{string, string, int}) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOptsInlinePointer(msg: "hi")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsInlinePointer":"hi"}`, out)

		out, err = modGen.With(daggerQueryAt(".", `{echoOptsInlinePointer(msg: "hi", suffix: "!", times: 2)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsInlinePointer":"hi!hi!"}`, out)
	})

	t.Run("func EchoOptsInlineCtx(ctx, struct{string, string, int}) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOptsInlineCtx(msg: "hi")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsInlineCtx":"hi"}`, out)

		out, err = modGen.With(daggerQueryAt(".", `{echoOptsInlineCtx(msg: "hi", suffix: "!", times: 2)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsInlineCtx":"hi!hi!"}`, out)
	})

	t.Run("func EchoOptsInlineTags(struct{string, string, int}) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOptsInlineTags(msg: "hi")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsInlineTags":"hi"}`, out)

		out, err = modGen.With(daggerQueryAt(".", `{echoOptsInlineTags(msg: "hi", suffix: "!", times: 2)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsInlineTags":"hi!hi!"}`, out)
	})

	t.Run("func EchoOptsPragmas(string, string, int) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echoOptsPragmas(msg: "hi")}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echoOptsPragmas":"hi...hi...hi..."}`, out)
	})
}

func (GoSuite) TestSignaturesBuiltinTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/builtin-types")

	out, err := modGen.With(daggerQueryAt(".", `{directory{withNewFile(path: "foo", contents: "bar"){id}}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.withNewFile.id").String()

	t.Run("func Read(ctx, Directory) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", fmt.Sprintf(`{read(dir: "%s")}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"read":"bar"}`, out)
	})

	t.Run("func ReadPointer(ctx, *dagger.Directory) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", fmt.Sprintf(`{readPointer(dir: "%s")}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"readPointer":"bar"}`, out)
	})

	t.Run("func ReadSlice(ctx, []Directory) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", fmt.Sprintf(`{readSlice(dir: ["%s"])}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"readSlice":"bar"}`, out)
	})

	t.Run("func ReadVariadic(ctx, ...Directory) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", fmt.Sprintf(`{readVariadic(dir: ["%s"])}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"readVariadic":"bar"}`, out)
	})

	t.Run("func ReadOptional(ctx, Optional[Directory]) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", fmt.Sprintf(`{readOptional(dir: "%s")}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"readOptional":"bar"}`, out)
		out, err = modGen.With(daggerQueryAt(".", `{readOptional}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"readOptional":""}`, out)
	})
}

func (GoSuite) TestSignaturesUnexported(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := moduleFixture(t, c, "go/unexported-root-only")

	objs := inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 1, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())

	modGen = moduleFixture(t, c, "go/unexported-return")

	objs = inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 2, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())
	require.Equal(t, "MinimalFoo", objs.Get("1.name").String())

	modGen = moduleFixture(t, c, "go/unexported-field-error")

	_, err := modGen.With(moduleIntrospection).Stderr(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	require.Regexp(t, "cannot code-generate unexported type bar", logs.String())
}

func (GoSuite) TestSignaturesMixMatch(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := moduleFixture(t, c, "go/mix-match")

	_, err := modGen.With(daggerQueryAt(".", `{hello}`)).Stdout(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	require.Regexp(t, "nested structs are not supported", logs.String())
}

func (GoSuite) TestSignaturesNameConflict(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := moduleFixture(t, c, "go/name-conflict")

	objs := inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 4, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())
	require.Equal(t, "MinimalFoo", objs.Get("1.name").String())
	require.Equal(t, "MinimalBar", objs.Get("2.name").String())
	require.Equal(t, "MinimalBaz", objs.Get("3.name").String())
}

func (GoSuite) TestDocs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/minimal")

	obj := inspectModuleObjects(ctx, t, modGen).Get("0")
	require.Equal(t, "Minimal", obj.Get("name").String())

	hello := obj.Get(`functions.#(name="hello")`)
	require.Equal(t, "hello", hello.Get("name").String())
	require.Empty(t, hello.Get("description").String())
	require.Empty(t, hello.Get("args").Array())

	// test the args-based form
	echoOpts := obj.Get(`functions.#(name="echoOpts")`)
	require.Equal(t, "echoOpts", echoOpts.Get("name").String())
	require.Equal(t, "EchoOpts does some opts things", echoOpts.Get("description").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "the message to echo", echoOpts.Get("args.0.description").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "Number of times to repeat the message", echoOpts.Get("args.2.description").String())

	// test the inline struct form
	echoOpts = obj.Get(`functions.#(name="echoOptsInline")`)
	require.Equal(t, "echoOptsInline", echoOpts.Get("name").String())
	require.Equal(t, "EchoOptsInline does some opts things", echoOpts.Get("description").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "the message to echo", echoOpts.Get("args.0.description").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "Number of times to repeat the message", echoOpts.Get("args.2.description").String())

	// test the arg-based form (with pragmas)
	echoOpts = obj.Get(`functions.#(name="echoOptsPragmas")`)
	require.Equal(t, "echoOptsPragmas", echoOpts.Get("name").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "", echoOpts.Get("args.0.defaultValue").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "\"...\"", echoOpts.Get("args.1.defaultValue").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "3", echoOpts.Get("args.2.defaultValue").String())
	require.Equal(t, "Number of times to repeat the message", echoOpts.Get("args.2.description").String())
}

func (GoSuite) TestDocsEdgeCases(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/docs-edge-cases")

	obj := inspectModuleObjects(ctx, t, modGen).Get("0")
	require.Equal(t, "Minimal", obj.Get("name").String())
	require.Equal(t, "Minimal is a thing", obj.Get("description").String())

	hello := obj.Get(`functions.#(name="hello")`)
	require.Equal(t, "hello", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 5)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "", hello.Get("args.1.description").String())
	require.Equal(t, "baz", hello.Get("args.2.name").String())
	require.Equal(t, "hello", hello.Get("args.2.description").String())
	require.Equal(t, "qux", hello.Get("args.3.name").String())
	require.Equal(t, "", hello.Get("args.3.description").String())
	require.Equal(t, "x", hello.Get("args.4.name").String())
	require.Equal(t, "lol", hello.Get("args.4.description").String())

	hello = obj.Get(`functions.#(name="helloMore")`)
	require.Equal(t, "helloMore", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 2)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "foo here", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "bar here", hello.Get("args.1.description").String())

	hello = obj.Get(`functions.#(name="helloMoreInline")`)
	require.Equal(t, "helloMoreInline", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 2)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "foo here", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "", hello.Get("args.1.description").String())

	hello = obj.Get(`functions.#(name="helloAgain")`)
	require.Equal(t, "helloAgain", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 3)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "docs for bar", hello.Get("args.1.description").String())
	require.Equal(t, "baz", hello.Get("args.2.name").String())
	require.Equal(t, "", hello.Get("args.2.description").String())

	hello = obj.Get(`functions.#(name="helloFinal")`)
	require.Equal(t, "helloFinal", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 1)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())

	require.Len(t, obj.Get(`fields`).Array(), 2)
	prop := obj.Get(`fields.#(name="x")`)
	require.Equal(t, "x", prop.Get("name").String())
	require.Equal(t, "X is this", prop.Get("description").String())
	prop = obj.Get(`fields.#(name="y")`)
	require.Equal(t, "y", prop.Get("name").String())
	require.Equal(t, "", prop.Get("description").String())
}

func (GoSuite) TestPragmaParsing(ctx context.Context, t *testctx.T) {
	// corner cases of pragma parsing

	c := connect(ctx, t)

	// corner cases where a +default pragma has a value that itself has a + in it
	modGen := moduleFixture(t, c, "go/pragma-parsing")

	out, err := modGen.With(daggerCallAt(".", "hello")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, `blah+dagger-ci@dagger.io`, out)
}

func (GoSuite) TestWeirdFields(ctx context.Context, t *testctx.T) {
	// these are all cases that used to panic due to the disparity in the type spec and the ast

	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/weird-fields")

	out, err := modGen.With(daggerQueryAt(".", `{w, x, y, z}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"w": "-", "x": "-", "y": "-", "z": "-"}`, out)

	for _, name := range []string{"say", "sayOpts"} {
		out, err := modGen.With(daggerQueryAt(".", `{%s(a: "hello", b: "world", c: "!")}`, name)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"%s": "hello world !"}`, name), out)
	}

	for _, name := range []string{"hello", "helloOpts"} {
		out, err := modGen.With(daggerQueryAt(".", `{%s(string: "")}`, name)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"%s": "hello"}`, name), out)
	}
}

func (GoSuite) TestFieldMustBeNil(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/field-must-be-nil")

	out, err := modGen.With(daggerQueryAt(".", `{isEmpty}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"isEmpty": true}`, out)
}

func (GoSuite) TestPrivateEnumField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := moduleFixture(t, c, "go/private-enum-field")

	out, err := ctr.With(daggerQueryAt(".", `{publish}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"publish": "registry/repo:latest"}`, out)
}

func (GoSuite) TestJSONField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/json-field")

	out, err := modGen.With(daggerQueryAt(".", `{config}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"config":"{\"a\":1}"}`, out)
}

// this is no longer allowed, but verify the Engine errors out
func (GoSuite) TestExtendCore(ctx context.Context, t *testctx.T) {
	moreContents := `package dagger

import (
	"context"
)

func (c *Container) Echo(ctx context.Context, msg string) (string, error) {
	return c.WithExec([]string{"echo", msg}).Stdout(ctx)
}
`

	t.Run("in different mod name", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))
		_, err := moduleFixture(t, c, "go/pragma-parsing").
			WithoutFile("/work/.gitignore"). // Remove .gitignore so we can override files inside internal/dagger without ignoring them.
			WithNewFile("/work/internal/dagger/more.go", moreContents).
			With(daggerQueryAt(".", `{container{from(address:"`+alpineImage+`"){echo(msg:"echo!"){stdout}}}}`)).
			Sync(ctx)
		require.Error(t, err)
		require.NoError(t, c.Close())
		t.Log(logs.String())

		// With lazy module loading, the error is no longer thrown by the SDK but directly by the engine
		// when evaluating the query against the engine GQL schema.
		require.Contains(t, logs.String(), `Cannot query field \"echo\" on type \"Container\"`)
	})

	t.Run("in same mod name", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))
		_, err := moduleFixture(t, c, "go/extend-core-container").
			WithoutFile("/work/.gitignore"). // Remove .gitignore so we can override files inside internal/dagger without ignoring them.
			WithNewFile("/work/internal/dagger/more.go", moreContents).
			With(daggerQueryAt(".", `{container{from(address:"`+alpineImage+`"){echo(msg:"echo!"){stdout}}}}`)).
			Sync(ctx)
		require.Error(t, err)
		require.NoError(t, c.Close())
		t.Log(logs.String())
		// With self calls always enabled for Go, a module type shadowing a
		// core type no longer fails the load; the core type keeps winning in
		// the client schema, so the extension method is simply absent — same
		// engine-side validation error as the different-mod-name case.
		require.Contains(t, logs.String(), `Cannot query field \"echo\" on type \"Container\"`)
	})
}

func (GoSuite) TestBadCtx(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	_, err := moduleFixture(t, c, "go/bad-ctx").
		With(daggerQueryAt(".", `{echo}`)).
		Sync(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	t.Log(logs.String())
	require.Regexp(t, "unexpected context type", logs.String())
}

func (GoSuite) TestWithOtherModuleTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		With(withModuleFixture(t, c, ".", "go/other-module-types")).
		WithWorkdir("/work/test")

	t.Run("return as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				With(withTestdataFile(t, c, "main.go", "modules", "go", "other-module-types", "cases", "return-direct.go")).
				With(daggerFunctions("-m", ".")).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q function %q cannot return external type from dependency module %q",
				"Test", "Fn", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				With(withTestdataFile(t, c, "main.go", "modules", "go", "other-module-types", "cases", "return-list.go")).
				With(daggerFunctions("-m", ".")).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q function %q cannot return external type from dependency module %q",
				"Test", "Fn", "dep",
			))
		})
	})

	t.Run("arg as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(withTestdataFile(t, c, "main.go", "modules", "go", "other-module-types", "cases", "arg-direct.go")).
				With(daggerFunctions("-m", ".")).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "Fn", "obj", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(withTestdataFile(t, c, "main.go", "modules", "go", "other-module-types", "cases", "arg-list.go")).
				With(daggerFunctions("-m", ".")).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "Fn", "obj", "dep",
			))
		})
	})

	t.Run("field as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				With(withTestdataFile(t, c, "main.go", "modules", "go", "other-module-types", "cases", "field-direct.go")).
				With(daggerFunctions("-m", ".")).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q field %q cannot reference external type from dependency module %q",
				"Obj", "Foo", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				With(withTestdataFile(t, c, "main.go", "modules", "go", "other-module-types", "cases", "field-list.go")).
				With(daggerFunctions("-m", ".")).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q field %q cannot reference external type from dependency module %q",
				"Obj", "Foo", "dep",
			))
		})
	})
}

func (GoSuite) TestUseDaggerTypesDirect(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := moduleFixture(t, c, "go/use-dagger-types-direct")

	out, err := modGen.With(daggerQueryAt(".", `{directory{id}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.id").String()

	out, err = modGen.With(daggerQueryAt(".", `{foo(dir: "%s"){file(path: "foo"){contents}}}`, dirID)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"file":{"contents": "xxx"}}}`, out)

	out, err = modGen.With(daggerQueryAt(".", `{bar(dir: "%s"){file(path: "bar"){contents}}}`, dirID)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"bar":{"file":{"contents": "yyy"}}}`, out)
}

func (GoSuite) TestUtilsPkg(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := moduleFixture(t, c, "go/utils-pkg")

	out, err := modGen.With(daggerQueryAt(".", `{hello}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"hello":"hello world"}`, out)
}

func (GoSuite) TestNameCase(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		With(withModuleFixture(t, c, "/toplevel", "go/name-case")).
		WithWorkdir("/toplevel/ssh")
	out, err := ctr.With(daggerQueryAt(".", `{sayHello}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"sayHello":"hello!"}`, out)

	ctr = ctr.
		WithWorkdir("/toplevel")
	out, err = ctr.With(daggerQueryAt(".", `{sayHello}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"sayHello":"hello!"}`, out)
}

func (GoSuite) TestEmbedded(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		With(withModuleFixture(t, c, "/playground", "go/embedded")).
		WithWorkdir("/playground")

	out, err := ctr.With(daggerQueryAt(".", `{sayHello, directory{entries}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"sayHello":"hello!", "directory":{"entries": []}}`, out)
}

func (GoSuite) TestContainerDefaultValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/container-default-value")

	out, err := modGen.With(daggerCallAt(".", "test-with-default-container")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "3.19") // Alpine version contains a dot like "3.19.0"

	// Test that we can override the default
	out2, err := modGen.With(daggerCallAt(".", "test-with-default-container", "--ctr=alpine:3.18")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out2, "3.18")
}
