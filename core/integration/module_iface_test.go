package core

// These tests cover Dagger interfaces declared by modules across SDKs. They
// verify interface definitions, implementation objects, and module functions
// that accept or return interface values.
//
// See also:
// - module_type_test.go: concrete custom types in module APIs.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

type InterfaceSuite struct{}

func TestInterface(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(InterfaceSuite{})
}

func (InterfaceSuite) TestIfaceBasic(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk  string
		path string
	}

	for _, tc := range []testCase{
		{sdk: "go", path: "./testdata/modules/go/ifaces"},
		{sdk: "typescript", path: "./testdata/modules/typescript/ifaces"},
		{sdk: "python", path: "./testdata/modules/python/ifaces"},
		{sdk: "csharp", path: "./testdata/modules/csharp/ifaces"},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := core.NewQuery(c).Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithMountedDirectory("/work", core.NewQuery(c).Host().Directory(tc.path)).
				WithWorkdir("/work")
			if tc.sdk == "csharp" {
				sdkSrc, err := filepath.Abs("../../sdk/csharp")
				require.NoError(t, err)
				ctr = ctr.
					WithMountedDirectory("/work/sdk/csharp", core.NewQuery(c).Host().Directory(sdkSrc)).
					WithExec([]string{"apk", "add", "git"}).
					WithExec([]string{"git", "init"})
			}

			_, err := ctr.
				With(daggerCallAt(".", "test")).
				Sync(ctx)
			require.NoError(t, err)
		})
	}
}

func (InterfaceSuite) TestIfaceGoSadPaths(ctx context.Context, t *testctx.T) {
	t.Run("no dagger object embed", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))

		_, err := moduleFixture(t, c, "go/iface-bad-no-embed").
			With(daggerFunctions("-m", ".")).
			Sync(ctx)
		require.Error(t, err)
		require.NoError(t, c.Close())
		require.Regexp(t, `missing method .* from DaggerObject interface, which must be embedded in interfaces used in Functions and Objects`, logs.String())
	})
}

func (InterfaceSuite) TestIfaceGoDanglingInterface(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/dangling-interface")

	out, err := modGen.
		With(daggerQueryAt(".", `{hello}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"hello":"hello"}`, out)
}

func (InterfaceSuite) TestIfaceCall(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk         string
		depFixture  string
		testFixture string
	}

	tests := []testCase{
		{
			sdk:         "go",
			depFixture:  "go/iface-call-mallard",
			testFixture: "go/iface-call-test",
		},
		{
			sdk:         "typescript",
			depFixture:  "typescript/iface-call-mallard",
			testFixture: "typescript/iface-call-test",
		},
		{
			sdk:         "python",
			depFixture:  "python/iface-call-mallard",
			testFixture: "python/iface-call-test",
		},
		{
			sdk:         "csharp",
			depFixture:  "csharp/iface-call-mallard",
			testFixture: "csharp/iface-call-test",
		},
	}

	for _, tc := range tests {
		for _, rtc := range tests {
			// No need for every permutation, just within the same SDK and
			// with Go as a reference implementation.
			if tc.sdk != "go" && rtc.sdk != "go" && tc.sdk != rtc.sdk {
				continue
			}

			t.Run(fmt.Sprintf("%s implementation defined in %s", tc.sdk, rtc.sdk), func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				var ctr *core.Container
				if rtc.sdk == "csharp" {
					sdkSrc, err := filepath.Abs("../../sdk/csharp")
					require.NoError(t, err)
					// Keep /work itself from being a C# module so find-up
					// does not steal mallard's SDK load. Both modules live as
					// siblings and share /work/sdk/csharp, matching TestCsharp.
					ctr = goGitBase(t, c).
						With(withModuleFixture(t, c, "test", rtc.testFixture)).
						With(withModuleFixture(t, c, "mallard", tc.depFixture)).
						WithDirectory("/work/sdk/csharp", core.NewQuery(c).Host().Directory(sdkSrc))
					out, err := ctr.
						WithWorkdir("/work/mallard").
						With(daggerCallAt(".", "quack")).
						WithWorkdir("/work/test").
						With(daggerCallAt(".", "get-duck", "quack")).
						Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "mallard quack", strings.TrimSpace(out))
					return
				}
				if tc.sdk == "csharp" {
					sdkSrc, err := filepath.Abs("../../sdk/csharp")
					require.NoError(t, err)
					// Go (or other) caller at /work; C# mallard as a sibling.
					// This layout already passed for csharp_implementation_defined_in_go.
					ctr = goGitBase(t, c).
						With(withModuleFixture(t, c, ".", rtc.testFixture)).
						With(withModuleFixture(t, c, "mallard", tc.depFixture)).
						WithDirectory("/work/sdk/csharp", core.NewQuery(c).Host().Directory(sdkSrc))
					out, err := ctr.
						With(daggerCallAt("mallard", "quack")).
						With(daggerCallAt(".", "get-duck", "quack")).
						Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "mallard quack", strings.TrimSpace(out))
					return
				}

				ctr = core.NewQuery(c).Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(withModuleFixture(t, c, ".", rtc.testFixture)).
					With(withModuleFixture(t, c, "mallard", tc.depFixture))

				out, err := ctr.
					With(daggerCallAt("mallard", "quack")).
					With(daggerCallAt(".", "get-duck", "quack")).
					Stdout(ctx)

				require.NoError(t, err)
				require.Equal(t, "mallard quack", strings.TrimSpace(out))
			})
		}
	}
}
