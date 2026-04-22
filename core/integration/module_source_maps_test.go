package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module tooling metadata, though setup still relies on historical module helpers.
// Scope: Source-location metadata propagated from module source into generated bindings.
// Intent: Keep source-map coverage separate from engine-version behavior and the remaining module runtime umbrella coverage.

import (
	"context"
	"fmt"
	"regexp"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestTypedefSourceMaps(ctx context.Context, t *testctx.T) {
	goBaseSrc := `package main

type Test struct {}
    `

	tsBaseSrc := `import { object, func } from "@dagger.io/dagger"

@object()
export class Test {}`

	type languageMatch struct {
		golang     []string
		typescript []string
	}

	tcs := []struct {
		sdk     string
		src     string
		matches languageMatch
	}{
		{
			sdk: "go",
			src: `package main

import "context"

type Dep struct {
    FieldDef string
}

func (m *Dep) FuncDef(
	arg1 string,
	arg2 string, // +optional
) string {
    return ""
}

type MyEnum string
const (
    MyEnumA MyEnum = "MyEnumA"
    MyEnumB MyEnum = "MyEnumB"
)

type MyInterface interface {
	DaggerObject
	Do(ctx context.Context, val int) (string, error)
}

func (m *Dep) Collect(MyEnum, MyInterface) error {
    // force all the types here to be collected
    return nil
}
    `,
			matches: languageMatch{
				golang: []string{
					// struct
					`\ntype Dep struct { // dep \(../../dep/main.go:5:6\)\n`,
					// struct field
					`\nfunc \(.* \*Dep\) FieldDef\(.* // dep \(../../dep/main.go:6:5\)\n`,
					// struct func
					`\nfunc \(.* \*Dep\) FuncDef\(.* // dep \(../../dep/main.go:9:1\)\n`,
					// struct func arg
					`\n\s*Arg2 string // dep \(../../dep/main.go:11:2\)\n`,

					// enum
					`\ntype DepMyEnum string // dep \(../../dep/main.go:16:6\)\n`,
					// enum value
					`\n\s*DepMyEnumA DepMyEnum = "MyEnumA" // dep \(../../dep/main.go:18:5\)\n`,

					// interface
					`\ntype DepMyInterface struct { // dep \(../../dep/main.go:22:6\)\n`,
					// interface func
					`\nfunc \(.* \*DepMyInterface\) Do\(.* // dep \(../../dep/main.go:24:4\)\n`,
				},
				typescript: []string{
					// struct
					`export class Dep extends BaseClient { // dep \(../../../dep/main.go:5:6\)`,
					// struct field
					`fieldDef = async \(\): Promise<string> => { // dep \(../../../dep/main.go:6:5\)`,
					// struct func
					`\s*funcDef = async \(.*\s*opts\?: .* \/\/ dep \(../../../dep/main.go:9:1\) *\s*.*\/\/ dep \(../../../dep/main.go:9:1\)`,
					// struct func arg
					`\s*arg2\?: string // dep \(../../../dep/main.go:11:2\)`,

					// enum
					`export enum DepMyEnum { // dep \(../../../dep/main.go:16:6\)`,
					// enum value
					`\s*A = "MyEnumA", // dep \(../../../dep/main.go:18:5\)`,
				},
			},
		},
		{
			sdk: "typescript",
			src: `import { object, func } from "@dagger.io/dagger"

export enum MyEnum {
  A = "MyEnumA",
	B = "MyEnumB",
}

@object()
export class Dep {
  @func()
  fieldDef: string

  @func()
  funcDef(arg1: string, arg2?: string): string {
    return ""
  }

	@func()
	async collect(enumValue: MyEnum): Promise<void> {}
}`,
			matches: languageMatch{
				golang: []string{
					// struct
					`\ntype Dep struct { // dep \(../../dep/src/index.ts:9:14\)\n`,
					// struct field
					`\nfunc \(.* \*Dep\) FieldDef\(.* // dep \(../../dep/src/index.ts:11:3\)\n`,
					// struct func
					`\nfunc \(.* \*Dep\) FuncDef\(.* // dep \(../../dep/src/index.ts:14:3\)\n`,
					// struct func arg
					`\n\s*Arg2 string // dep \(../../dep/src/index.ts:14:25\)\n`,

					// enum
					`\ntype DepMyEnum string // dep \(../../dep/src/index.ts:3:13\)\n`,
					// enum value
					`\n\s*DepMyEnumA DepMyEnum = "MyEnumA" // dep \(../../dep/src/index.ts:4:3\)\n`,
				},
				typescript: []string{
					// struct
					`export class Dep extends BaseClient { // dep \(../../../dep/src/index.ts:9:14\)`,
					// struct field
					`\s*fieldDef = async \(\): Promise<string> => { // dep \(../../../dep/src/index.ts:11:3\)`,
					// struct func
					`\s*funcDef = async \(.*\s*opts\?: .* \/\/ dep \(../../../dep/src/index.ts:14:3\) *\s*.*\/\/ dep \(../../../dep/src/index.ts:14:3\)`,
					// struct func arg
					`\s*arg2\?: string // dep \(../../../dep/src/index.ts:14:25\)`,

					// enum
					`export enum DepMyEnum { // dep \(../../../dep/src/index.ts:3:13\)`,
					// enum value
					`\s*A = "MyEnumA", // dep \(../../../dep/src/index.ts:4:3\)`,
				},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(fmt.Sprintf("%s dep with go generation", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, "go", goBaseSrc).
				With(withModInitAt("./dep", tc.sdk, tc.src)).
				With(daggerExec("install", "./dep"))

			codegenContents, err := modGen.File("internal/dagger/dep.gen.go").Contents(ctx)
			require.NoError(t, err)

			for _, match := range tc.matches.golang {
				matched, err := regexp.MatchString(match, codegenContents)
				require.NoError(t, err)
				require.Truef(t, matched, "%s did not match contents:\n%s", match, codegenContents)
			}
		})

		t.Run(fmt.Sprintf("%s dep with typescript generation", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, "typescript", tsBaseSrc).
				With(withModInitAt("./dep", tc.sdk, tc.src)).
				With(daggerExec("install", "./dep"))

			codegenContents, err := modGen.File(sdkCodegenFile(t, "typescript")).Contents(ctx)
			require.NoError(t, err)

			for _, match := range tc.matches.typescript {
				matched, err := regexp.MatchString(match, codegenContents)
				require.NoError(t, err)
				require.Truef(t, matched, "%s did not match contents:\n%s", match, codegenContents)
			}
		})
	}
}
