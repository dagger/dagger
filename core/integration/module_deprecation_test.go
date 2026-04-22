package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module deprecation semantics, though setup still relies on historical module helpers.
// Scope: Deprecation metadata exposed through module introspection and validation rules for deprecated arguments.
// Intent: Keep module deprecation behavior separate from the remaining runtime, SDK, and dependency coverage in the historical umbrella suite.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestModuleDeprecationIntrospection(ctx context.Context, t *testctx.T) {
	type sdkCase struct {
		sdk        string
		writeFiles func(dir string) error
	}

	goSrc := `package main

import (
	"context"
)

// +deprecated="This module is deprecated and will be removed in future versions."
type Test struct {
	LegacyField string // +deprecated="This field is deprecated and will be removed in future versions."
}

// +deprecated="This type is deprecated and kept only for retro-compatibility."
type LegacyRecord struct {
	// +deprecated="This field is deprecated and will be removed in future versions."
	Note string
}

func (m *Test) EchoString(
	ctx context.Context,
	input *string, // +deprecated="Use 'other' instead of 'input'."
	other string,
) (string, error) {
	if input != nil {
		return *input, nil
	}
	return other, nil
}

// +deprecated="Prefer EchoString instead."
func (m *Test) LegacySummarize(note string) (LegacyRecord, error) {
	return LegacyRecord{Note: note}, nil
}

type Mode string

const (
	ModeAlpha Mode = "alpha" // +deprecated="alpha is deprecated; use zeta instead"
	// +deprecated="beta is deprecated; use zeta instead"
	ModeBeta Mode = "beta"
	ModeZeta Mode = "zeta"
)

// Reference the enum so it appears in the schema.
func (m *Test) UseMode(mode Mode) Mode {
	return mode
}

type Fooer interface {
	DaggerObject

	// +deprecated="Use Bar instead"
	Foo(ctx context.Context, value int) (string, error)

	Bar(ctx context.Context, value int) (string, error)
}

func (m *Test) CallFoo(ctx context.Context, foo Fooer, value int) (string, error) {
	return foo.Foo(ctx, value)
}`
	const tsSrc = `import { field, func, object } from "@dagger.io/dagger"

  /** @deprecated This module is deprecated and will be removed in future versions. */
  @object()
  export class Test {
    /** @deprecated This field is deprecated and will be removed in future versions. */
    @field()
    legacyField = "legacy"

    @func()
    async echoString(
	  other: string,
      /** @deprecated Use 'other' instead of 'input'. */
      input?: string,
    ): Promise<string> {
      return input ?? other
    }

    /** @deprecated Prefer EchoString instead. */
    @func()
    async legacySummarize(note: string): Promise<LegacyRecord> {
      return { note }
    }

    @func()
    useMode(mode: Mode): Mode {
      return mode
    }

	@func()
	async callFoo(foo: Fooer, value: number): Promise<string> {
		return foo.foo(value)
	}
  }

  /** @deprecated This type is deprecated and kept only for retro-compatibility. */
  export type LegacyRecord = {
    /** @deprecated This field is deprecated and will be removed in future versions. */
    note: string
  }

  export enum Mode {
    /** @deprecated alpha is deprecated; use zeta instead */
    Alpha = "alpha",
    /** @deprecated beta is deprecated; use zeta instead */
    Beta = "beta",
    Zeta = "zeta",
  }

  export interface Fooer {
    /** @deprecated Use Bar instead */
    foo(value: number): Promise<string>

    bar(value: number): Promise<string>
  }`

	const pySrc = `import enum
import typing
from typing import Annotated, Optional

import dagger

@dagger.object_type(
    deprecated="This module is deprecated and will be removed in future versions."
)
class Test:
    legacy_field: str = dagger.field(
        name="legacyField",
        deprecated="This field is deprecated and will be removed in future versions.",
    )

    @dagger.function(name="echoString")
    def echo_string(
        self,
        input: Annotated[
            Optional[str], dagger.Deprecated("Use 'other' instead of 'input'.")
        ],
        other: str,
    ) -> str:
        return input if input is not None else other

    @dagger.function(name="legacySummarize", deprecated="Prefer EchoString instead.")
    def legacy_summarize(self, note: str) -> "LegacyRecord":
        return LegacyRecord(note=note)

    @dagger.function(name="useMode")
    def use_mode(self, mode: "Mode") -> "Mode":
        return mode

    @dagger.function(name="callFoo")
    async def call_foo(self, foo: "Fooer", value: int) -> str:
        return await foo.foo(value)



@dagger.object_type(
    deprecated="This type is deprecated and kept only for retro-compatibility."
)
class LegacyRecord:
    note: str = dagger.field(
        deprecated="This field is deprecated and will be removed in future versions."
    )


@dagger.enum_type
class Mode(enum.Enum):
    """Mode is deprecated; use zeta instead."""

    ALPHA = "alpha"
    """Alpha mode.

    .. deprecated:: alpha is deprecated; use zeta instead
    """

    BETA = "beta"
    """Beta mode.

    .. deprecated:: beta is deprecated; use zeta instead
    """

    ZETA = "zeta"
    """ infos """

@dagger.interface
class Fooer(typing.Protocol):
    @dagger.function(deprecated="Use Bar instead")
    async def foo(self, value: int) -> str: ...

    @dagger.function()
    async def bar(self, value: int) -> str: ...
`

	cases := []sdkCase{
		{
			sdk: "go",
			writeFiles: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, "main.go"), []byte(goSrc), 0o644)
			},
		},
		{
			sdk: "typescript",
			writeFiles: func(dir string) error {
				srcDir := filepath.Join(dir, "src")
				if err := os.MkdirAll(srcDir, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(srcDir, "index.ts"), []byte(tsSrc), 0o644)
			},
		},
		{
			sdk: "python",
			writeFiles: func(dir string) error {
				pyDir := filepath.Join(dir, "src", "test")
				if err := os.MkdirAll(pyDir, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(pyDir, "__init__.py"), []byte(pySrc), 0o644)
			},
		},
	}

	type Arg struct {
		Name       string
		Deprecated string
	}
	type Fn struct {
		Name       string
		Deprecated string
		Args       []Arg
	}
	type Field struct {
		Name       string
		Deprecated string
	}
	type Obj struct {
		Name       string
		Deprecated string
		Functions  []Fn
		Fields     []Field
	}
	type EnumMember struct {
		Value      string
		Deprecated string
	}
	type Enum struct {
		Name    string
		Members []EnumMember
	}
	type Iface struct {
		Name      string
		Functions []Fn
	}
	type Resp struct {
		Host struct {
			Directory struct {
				AsModule struct {
					Objects    []struct{ AsObject Obj }
					Enums      []struct{ AsEnum Enum }
					Interfaces []struct{ AsInterface Iface }
				}
			}
		}
	}

	const introspect = `
query ModuleIntrospection($path: String!) {
  host {
    directory(path: $path) {
      asModule {
        objects {
          asObject {
            name
            deprecated
            functions { name deprecated args { name deprecated } }
            fields { name deprecated }
          }
        }
        enums { asEnum { name members { value deprecated } } }
        interfaces {
          asInterface {
            name
            functions { name deprecated args { name } }
          }
        }
      }
    }
  }
}`

	for _, tc := range cases {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			modDir := t.TempDir()

			_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk="+tc.sdk)
			require.NoError(t, err)
			require.NoError(t, tc.writeFiles(modDir))

			c := connect(ctx, t)

			res, err := testutil.QueryWithClient[Resp](c, t, introspect, &testutil.QueryOptions{
				Variables: map[string]any{"path": modDir},
			})
			require.NoError(t, err)

			var testObj, legacyObj *Obj
			for i := range res.Host.Directory.AsModule.Objects {
				o := &res.Host.Directory.AsModule.Objects[i].AsObject
				switch o.Name {
				case "Test":
					testObj = o
				case "TestLegacyRecord":
					legacyObj = o
				}
			}
			require.NotNil(t, testObj, "Test object must be present")
			require.Equal(t, "This module is deprecated and will be removed in future versions.", testObj.Deprecated, "Test object must be marked deprecated")

			legacyField := &testObj.Fields[0]
			require.NotNil(t, legacyField, "Test.LegacyField must be present")
			require.Equal(t, "This field is deprecated and will be removed in future versions.", legacyField.Deprecated, "Test.LegacyField must be marked deprecated")

			fnByName := map[string]Fn{}
			for _, f := range testObj.Functions {
				fnByName[f.Name] = f
			}

			ls, ok := fnByName["legacySummarize"]
			require.True(t, ok, "legacySummarize function must be present")
			require.Equal(t, "Prefer EchoString instead.", ls.Deprecated, "legacySummarize function must be marked deprecated")

			ech, ok := fnByName["echoString"]
			require.True(t, ok, "echoString function must be present")
			require.Empty(t, ech.Deprecated, "echoString function must not be deprecated")

			var inputArg, otherArg *Arg
			for i := range ech.Args {
				switch ech.Args[i].Name {
				case "input":
					inputArg = &ech.Args[i]
				case "other":
					otherArg = &ech.Args[i]
				}
			}
			require.NotNil(t, inputArg, "echoString should have arg 'input'")
			require.Equal(t, "Use 'other' instead of 'input'.", inputArg.Deprecated, "echoString.input should be marked deprecated")
			require.NotNil(t, otherArg, "echoString should have arg 'other'")
			require.Empty(t, otherArg.Deprecated, "echoString.other should NOT be deprecated")

			require.NotNil(t, legacyObj, "LegacyRecord object must be present")
			require.Equal(t, "This type is deprecated and kept only for retro-compatibility.", legacyObj.Deprecated, "LegacyRecord must be marked deprecated")

			var noteField *Field
			for i := range legacyObj.Fields {
				if legacyObj.Fields[i].Name == "note" {
					noteField = &legacyObj.Fields[i]
					break
				}
			}
			require.NotNil(t, noteField, "LegacyRecord should have field 'note'")
			require.Equal(t, "This field is deprecated and will be removed in future versions.", noteField.Deprecated, "LegacyRecord.note must be marked deprecated")

			mode := &res.Host.Directory.AsModule.Enums[0]
			require.NotNil(t, mode)

			m := mode.AsEnum
			var alpha, beta, zeta *EnumMember
			for i := range m.Members {
				switch m.Members[i].Value {
				case "alpha":
					alpha = &m.Members[i]
				case "beta":
					beta = &m.Members[i]
				case "zeta":
					zeta = &m.Members[i]
				}
			}
			require.NotNil(t, alpha, "Mode should have member 'alpha'")
			require.Equal(t, "alpha is deprecated; use zeta instead", alpha.Deprecated, "Mode.alpha must be marked deprecated")
			require.NotNil(t, beta, "Mode should have member 'beta'")
			require.Equal(t, "beta is deprecated; use zeta instead", beta.Deprecated, "Mode.beta must be marked deprecated")
			require.NotNil(t, zeta, "Mode should have member 'zeta'")
			require.Empty(t, zeta.Deprecated, "Mode.zeta should NOT be deprecated")

			var fooer *Iface
			for i := range res.Host.Directory.AsModule.Interfaces {
				iFace := &res.Host.Directory.AsModule.Interfaces[i].AsInterface
				if iFace.Name == "TestFooer" {
					fooer = iFace
					break
				}
			}
			require.NotNil(t, fooer, "test interface must be present")

			fnByNameIface := map[string]Fn{}
			for _, f := range fooer.Functions {
				fnByNameIface[f.Name] = f
			}

			fooFn, ok := fnByNameIface["foo"]
			require.True(t, ok, "TestFooer.foo must be present")
			require.Equal(t, "Use Bar instead", fooFn.Deprecated, "TestFooer.foo must be marked deprecated")

			var valueArg *Arg
			for i := range fooFn.Args {
				if fooFn.Args[i].Name == "value" {
					valueArg = &fooFn.Args[i]
					break
				}
			}
			require.NotNil(t, valueArg, "TestFooer.foo must have arg 'value'")
		})
	}
}

func (ModuleSuite) TestModuleDeprecationValidationErrors(ctx context.Context, t *testctx.T) {
	const introspect = `
query ModuleIntrospection($path: String!) {
  host {
    directory(path: $path) {
      asModule {
        objects {
          asObject {
            name
            deprecated
            functions { name deprecated args { name deprecated } }
            fields { name deprecated }
          }
        }
        enums { asEnum { name members { value deprecated } } }
        interfaces {
          asInterface {
            name
            functions { name deprecated args { name } }
          }
        }
      }
    }
  }
}`

	invalidCases := []struct {
		sdk        string
		contents   string
		errorMatch string
	}{
		{
			sdk: "go",
			contents: `package main

import "context"

type Test struct{}

func (m *Test) Legacy(
	ctx context.Context,
	input string, // +deprecated="Use other instead"
	other string,
) (string, error) {
	return input, nil
}
`,
			errorMatch: "argument \"input\" on Test.Legacy is required and cannot be deprecated",
		},
		{
			sdk: "typescript",
			contents: `import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async legacy(
    /** @deprecated Use 'other' instead. */
    input: string,
    other: string,
  ): Promise<string> {
    return input
  }
}
`,
			errorMatch: "argument input is required and cannot be deprecated",
		},
		{
			sdk: "python",
			contents: `import dagger
from typing import Annotated


@dagger.object_type
class Test:
    @dagger.function
    def legacy(
        self,
        input: Annotated[str, dagger.Deprecated("Use other instead")],
        other: str,
    ) -> str:
        return input
`,
			errorMatch: "Can't deprecate required parameter 'input'",
		},
	}

	type Arg struct {
		Name       string
		Deprecated string
	}
	type Fn struct {
		Name       string
		Deprecated string
		Args       []Arg
	}
	type Field struct {
		Name       string
		Deprecated string
	}
	type Obj struct {
		Name       string
		Deprecated string
		Functions  []Fn
		Fields     []Field
	}
	type EnumMember struct {
		Value      string
		Deprecated string
	}
	type Enum struct {
		Name    string
		Members []EnumMember
	}
	type Iface struct {
		Name      string
		Functions []Fn
	}
	type Resp struct {
		Host struct {
			Directory struct {
				AsModule struct {
					Objects    []struct{ AsObject Obj }
					Enums      []struct{ AsEnum Enum }
					Interfaces []struct{ AsInterface Iface }
				}
			}
		}
	}

	for _, tc := range invalidCases {
		t.Run(fmt.Sprintf("%s rejects deprecated required arguments", tc.sdk), func(ctx context.Context, t *testctx.T) {
			modDir := t.TempDir()

			_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk="+tc.sdk)
			require.NoError(t, err)

			target := filepath.Join(modDir, sdkSourceFile(tc.sdk))
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte(tc.contents), 0o644))

			c := connect(ctx, t)

			_, err = testutil.QueryWithClient[Resp](c, t, introspect, &testutil.QueryOptions{
				Variables: map[string]any{"path": modDir},
			})
			require.Error(t, err)

			errMsg := err.Error()
			var execErr *dagger.ExecError
			if errors.As(err, &execErr) {
				errMsg = fmt.Sprintf("%s\nStdout: %s\nStderr: %s", err, execErr.Stdout, execErr.Stderr)
			}

			if strings.Contains(errMsg, "failed to run command [docker info]") ||
				strings.Contains(errMsg, "socket: operation not permitted") ||
				strings.Contains(errMsg, "permission denied while trying to connect to the Docker daemon") {
				t.Skipf("engine unavailable: %s", errMsg)
				return
			}

			require.Containsf(t, errMsg, tc.errorMatch, "unexpected error message: %s", errMsg)
		})
	}

	validCases := []struct {
		sdk      string
		contents string
	}{
		{
			sdk: "go",
			contents: `package main

import "context"

type Test struct{}

func (m *Test) Legacy(
	ctx context.Context,
	input string, // +default="\"legacy\"" +deprecated="Use other instead"
	other string,
) (string, error) {
	return input, nil
}
`,
		},
		{
			sdk: "go",
			contents: `package main

import "context"

type Test struct{}

func (m *Test) Legacy(
	ctx context.Context,
	input ...string, // +deprecated="Use other instead"
) (string, error) {
	if len(input) > 0 {
		return input[0], nil
	}
	return "", nil
}
`,
		},
		// todo(guillaume): re-enable once we have a way to resolve external libs default values in TS
		// https://github.com/dagger/dagger/pull/11319
		// 		{
		// 			sdk: "typescript",
		// 			contents: `import { func, object } from "@dagger.io/dagger"

		// @object()
		// export class Test {
		//   @func()
		//   async legacy(
		//     /** @deprecated Use 'other' instead. */
		//     input: string = "legacy",
		//     other: string,
		//   ): Promise<string> {
		//     return input
		//   }
		// }
		// `,
		// 		},
		{
			sdk: "typescript",
			contents: `import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async legacy(
    /** @deprecated Prefer providing inputs via 'other'. */
    ...input: string[]
  ): Promise<string> {
    return input[0] ?? ""
  }
}
`,
		},
		{
			sdk: "python",
			contents: `import dagger
from typing import Annotated


@dagger.object_type
class Test:
    @dagger.function
    def legacy(
        self,
        input: Annotated[str, dagger.Deprecated("Use other instead")] = "legacy",
        other: str = "other",
    ) -> str:
        return input
`,
		},
	}

	for _, tc := range validCases {
		t.Run(fmt.Sprintf("%s allows deprecated optional arguments", tc.sdk), func(ctx context.Context, t *testctx.T) {
			modDir := t.TempDir()

			_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk="+tc.sdk)
			require.NoError(t, err)

			target := filepath.Join(modDir, sdkSourceFile(tc.sdk))
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte(tc.contents), 0o644))

			c := connect(ctx, t)

			_, err = testutil.QueryWithClient[Resp](c, t, introspect, &testutil.QueryOptions{
				Variables: map[string]any{"path": modDir},
			})
			if err != nil {
				errMsg := err.Error()
				if strings.Contains(errMsg, "failed to run command [docker info]") ||
					strings.Contains(errMsg, "socket: operation not permitted") ||
					strings.Contains(errMsg, "permission denied while trying to connect to the Docker daemon") {
					t.Skipf("engine unavailable: %s", errMsg)
					return
				}
			}
			require.NoError(t, err)
		})
	}
}
