package core

// These tests cover how authored module source becomes a Dagger API. They
// verify SDK selection, schema descriptions, field visibility, optional/default
// argument registration, generated bindings for optionals, and global `dag`
// references in module code.
//
// See also:
// - module_validation_test.go: invalid API shapes and reserved names.
// - module_type_test.go: custom types exposed by module APIs.

import (
	"context"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func (ModuleSuite) TestInvalidSDK(ctx context.Context, t *testctx.T) {
	t.Run("invalid sdk returns readable error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "invalid-sdk/foo-bar")

		_, err := modGen.
			With(daggerQuery(`{containerEcho(stringArg:"hello"){stdout}}`)).
			Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, `invalid SDK: "foo-bar"`)
	})

	t.Run("specifying version with either of go/python/typescript sdk returns error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := moduleFixture(t, c, "invalid-sdk/go-version")

		_, err := modGen.
			With(daggerQuery(`{containerEcho(stringArg:"hello"){stdout}}`)).
			Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, `the go sdk does not currently support selecting a specific version`)
	})
}

func (ModuleSuite) TestDescription(ctx context.Context, t *testctx.T) {
	for i, tc := range []struct {
		sdk     string
		fixture string
		files   int
	}{
		{
			sdk:     "go",
			fixture: "go/description-single",
			files:   1,
		},
		{
			sdk:     "go",
			fixture: "go/description-multi",
			files:   2,
		},
		{
			sdk:     "python",
			fixture: "python/description-single",
			files:   1,
		},
		{
			sdk:     "python",
			fixture: "python/description-multi",
			files:   2,
		},
		{
			sdk:     "typescript",
			fixture: "typescript/description-single",
			files:   1,
		},
		{
			sdk:     "typescript",
			fixture: "typescript/description-multi",
			files:   2,
		},
	} {
		t.Run(fmt.Sprintf("%s with %d files (#%d)", tc.sdk, tc.files, i+1), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			mod := inspectModule(ctx, t, moduleFixture(t, c, tc.fixture))

			require.Equal(t,
				"Test module, short description\n\nLong description, with full sentences.",
				mod.Get("description").String(),
			)
			require.Equal(t,
				"Test object, short description",
				mod.Get("objects.#.asObject|#(name=Test).description").String(),
			)
		})
	}
}

func (ModuleSuite) TestPrivateField(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk     string
		fixture string
	}{
		{
			sdk:     "go",
			fixture: "go/private-field",
		},
		{
			sdk:     "python",
			fixture: "python/private-field",
		},
		{
			sdk:     "typescript",
			fixture: "typescript/private-field",
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := moduleFixture(t, c, tc.fixture)

			obj := inspectModuleObjects(ctx, t, modGen).Get("0")
			require.Equal(t, "Test", obj.Get("name").String())
			require.Len(t, obj.Get(`fields`).Array(), 1)
			prop := obj.Get(`fields.#(name="foo")`)
			require.Equal(t, "foo", prop.Get("name").String())

			out, err := modGen.With(daggerQuery(`{set(foo: "abc", bar: "xyz"){hello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"set":{"hello": "abcxyz"}}`, out)

			out, err = modGen.With(daggerQuery(`{set(foo: "abc", bar: "xyz"){foo}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"set":{"foo": "abc"}}`, out)

			_, err = modGen.With(daggerQuery(`{set(foo: "abc", bar: "xyz"){bar}}`)).Stdout(ctx)
			requireErrOut(t, err, `Cannot query field \"bar\" on type \"Test\"`)
		})
	}
}

func (ModuleSuite) TestOptionalDefaults(ctx context.Context, t *testctx.T) {
	// Test expressiveness for following schema:
	//   a: String!
	//   b: String
	//   c: String! = "foo"
	//   d: String = null
	//   e: String = "bar"

	for _, tc := range []struct {
		sdk      string
		fixture  string
		expected string
	}{
		{
			sdk:      "go",
			fixture:  "go/optional-defaults",
			expected: "test, <nil>, foo, <nil>, bar",
		},
		{
			sdk:      "python",
			fixture:  "python/optional-defaults",
			expected: "'test', None, 'foo', None, 'bar'",
		},
		{
			sdk:      "typescript",
			fixture:  "typescript/optional-defaults",
			expected: "\"test\", , \"foo\", null, \"bar\"",
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := moduleFixture(t, c, tc.fixture)

			q := heredoc.Doc(`
                query {
                    __type(name: "Test") {
                        fields {
                            name
                            args {
                                name
                                type {
                                    name
                                    kind
                                    ofType {
                                        name
                                        kind
                                    }
                                }
                                defaultValue
                            }
                        }
                    }
                }
            `)

			out, err := modGen.With(daggerQuery(q)).Stdout(ctx)
			require.NoError(t, err)
			args := gjson.Get(out, "__type.fields.#(name=foo).args")

			t.Run("a: String!", func(ctx context.Context, t *testctx.T) {
				// required, i.e., non-null and no default
				arg := args.Get("#(name=a)")
				require.Equal(t, "NON_NULL", arg.Get("type.kind").String())
				require.Equal(t, "SCALAR", arg.Get("type.ofType.kind").String())
				require.Nil(t, arg.Get("defaultValue").Value())
			})

			t.Run("b: String", func(ctx context.Context, t *testctx.T) {
				// GraphQL implicitly sets default to null for nullable types
				arg := args.Get("#(name=b)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.Nil(t, arg.Get("defaultValue").Value())
			})

			t.Run(`c: String! = "foo"`, func(ctx context.Context, t *testctx.T) {
				// non-null, with default
				arg := args.Get("#(name=c)")
				require.Equal(t, "NON_NULL", arg.Get("type.kind").String())
				require.Equal(t, "SCALAR", arg.Get("type.ofType.kind").String())
				require.JSONEq(t, `"foo"`, arg.Get("defaultValue").String())
			})

			t.Run("d: String = null", func(ctx context.Context, t *testctx.T) {
				// nullable, with explicit null default; same as b in practice
				arg := args.Get("#(name=d)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.JSONEq(t, "null", arg.Get("defaultValue").String())
			})

			t.Run(`e: String = "bar"`, func(ctx context.Context, t *testctx.T) {
				// nullable, with non-null default
				arg := args.Get("#(name=e)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.JSONEq(t, `"bar"`, arg.Get("defaultValue").String())
			})

			t.Run("default values", func(ctx context.Context, t *testctx.T) {
				out, err = modGen.With(daggerCall("foo", "--a=test")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, tc.expected, out)
			})
		})
	}
}

func (ModuleSuite) TestCodegenOptionals(ctx context.Context, t *testctx.T) {
	// Same code as TestOptionalDefaults since it guarantees this is being
	// registered correctly and equally by all SDKs.
	expected := "foo, <nil>, foo, <nil>, bar"

	for _, tc := range []struct {
		sdk     string
		fixture string
	}{
		{
			sdk:     "go",
			fixture: "go/codegen-optionals",
		},
		{
			sdk:     "python",
			fixture: "python/codegen-optionals",
		},
		{
			sdk:     "typescript",
			fixture: "typescript/codegen-optionals",
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := moduleFixture(t, c, tc.fixture).
				With(withModuleFixture(t, c, "dep", "go/codegen-optionals-dep")).
				With(daggerCall("test")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, expected, out)
		})
	}
}

func (ModuleSuite) TestGlobalVarDAG(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk     string
		fixture string
	}

	for _, tc := range []testCase{
		{
			sdk:     "go",
			fixture: "go/global-var-dag",
		},
		{
			sdk:     "python",
			fixture: "python/global-var-dag",
		},
		{
			sdk:     "typescript",
			fixture: "typescript/global-var-dag",
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := moduleFixture(t, c, tc.fixture).
				With(daggerQuery(`{fn}`)).Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"fn":"foo\n"}`, out)
		})
	}
}
