package core

// These tests cover source-location comments written into generated module
// bindings. They verify that Go and TypeScript bindings point back to the
// module source lines that defined objects, fields, functions, args, enums, and
// interfaces.
//
// See also:
// - module_engine_version_test.go: module tooling metadata tied to engine version.
// - module_definition_test.go: module API definition behavior.

import (
	"context"
	"fmt"
	"regexp"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestTypedefSourceMaps(ctx context.Context, t *testctx.T) {
	type languageMatch struct {
		golang     []string
		typescript []string
	}

	tcs := []struct {
		sdk     string
		fixture string
		matches languageMatch
	}{
		{
			sdk:     "go",
			fixture: "go/source-map-dep",
			matches: languageMatch{
				golang: []string{
					// struct
					`\ntype Dep struct { // dep \(../../dep/main.go:5:6\)\n`,
					// struct field
					`\nfunc \(.* \*Dep\) FieldDef\(.* // dep \(../../dep/main.go:6:2\)\n`,
					// struct func
					`\nfunc \(.* \*Dep\) FuncDef\(.* // dep \(../../dep/main.go:9:1\)\n`,
					// struct func arg
					`\n\s*Arg2 string // dep \(../../dep/main.go:11:2\)\n`,

					// enum
					`\ntype DepMyEnum string // dep \(../../dep/main.go:16:6\)\n`,
					// enum value
					`\n\s*DepMyEnumA DepMyEnum = "MyEnumA" // dep \(../../dep/main.go:19:2\)\n`,

					// interface
					`\ntype DepMyInterface interface { // dep \(../../dep/main.go:23:6\)\n`,
					// interface func
					`\nfunc \(.* \*DepMyInterfaceClient\) Do\(.* // dep \(../../dep/main.go:25:4\)\n`,
				},
				typescript: []string{
					// struct
					`export class Dep extends BaseClient { // dep \(../../../dep/main.go:5:6\)`,
					// struct field
					`fieldDef = async \(\): Promise<string> => { // dep \(../../../dep/main.go:6:2\)`,
					// struct func
					`\s*funcDef = async \(.*\s*opts\?: .* \/\/ dep \(../../../dep/main.go:9:1\) *\s*.*\/\/ dep \(../../../dep/main.go:9:1\)`,
					// struct func arg
					`\s*arg2\?: string // dep \(../../../dep/main.go:11:2\)`,

					// enum
					`export enum DepMyEnum { // dep \(../../../dep/main.go:16:6\)`,
					// enum value
					`\s*A = "MyEnumA", // dep \(../../../dep/main.go:19:2\)`,
				},
			},
		},
		{
			sdk:     "typescript",
			fixture: "typescript/source-map-dep",
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

			modGen := goGitBase(t, c).
				With(withModuleFixture(t, c, ".", "go/source-map-root")).
				With(withModuleFixture(t, c, "dep", tc.fixture)).
				With(clientGeneratorWorkspaceClients(clientGeneratorSDKClientFor("go", "client"))).
				With(daggerExec("generate", "-y"))

			codegenContents, err := modGen.File("client/dep.gen.go").Contents(ctx)
			require.NoError(t, err)

			for _, match := range tc.matches.golang {
				matched, err := regexp.MatchString(match, codegenContents)
				require.NoError(t, err)
				require.Truef(t, matched, "%s did not match contents:\n%s", match, codegenContents)
			}
		})

		t.Run(fmt.Sprintf("%s dep with typescript generation", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				With(withModuleFixture(t, c, ".", "typescript/source-map-root")).
				With(withModuleFixture(t, c, "dep", tc.fixture)).
				With(clientGeneratorWorkspaceClients(clientGeneratorSDKClientFor("typescript", "sdk"))).
				With(daggerExec("generate", "-y"))

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
