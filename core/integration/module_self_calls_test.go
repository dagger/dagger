package core

// These tests cover a module invoking functions from its own Dagger API. They
// verify direct GraphQL self-queries and SDK-generated helpers for self-calls.
//
// See also:
// - module_current_module_test.go: `dag.CurrentModule()` introspection.
// - module_runtime_behavior_test.go: general module execution behavior.

import (
	"context"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestSelfAPICall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("module", "init", "--source=.", "--sdk=go", "test", ".")).
		WithNewFile("main.go", `package main

import (
	"context"

	"github.com/Khan/genqlient/graphql"
)

type Test struct{}

func (m *Test) FnA(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{test{fnB}}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["test"].(map[string]any)["fnB"].(string), nil
}

func (m *Test) FnB() string {
	return "hi from b"
}
`,
		).
		With(daggerQuery(`{fnA}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"fnA": "hi from b"}`, out)
}

func (ModuleSuite) TestSelfCalls(ctx context.Context, t *testctx.T) {
	tcs := []struct {
		sdk    string
		source string
	}{
		{
			sdk: "go",
			source: `package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ContainerEcho(
	// +optional
	// +default="Hello Self Calls"
	stringArg string,
) *dagger.Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}

func (m *Test) Print(ctx context.Context, stringArg string) (string, error) {
	return dag.Test().ContainerEcho(dagger.TestContainerEchoOpts{
		StringArg: stringArg,
	}).Stdout(ctx)
}

func (m *Test) PrintDefault(ctx context.Context) (string, error) {
	return dag.Test().ContainerEcho().Stdout(ctx)
}
`,
		},
		//		{
		//			sdk: "typescript",
		//			source: `import { dag, Container, object, func } from "@dagger.io/dagger"
		//
		// @object()
		// export class Test {
		//   /**
		//    * Returns a container that echoes whatever string argument is provided
		//    */
		//   @func()
		//   containerEcho(stringArg: string = "Hello Self Calls"): Container {
		//     return dag.container().from("alpine:latest").withExec(["echo", stringArg])
		//   }
		//
		//   @func()
		//   async print(stringArg: string): Promise<string> {
		//     return dag.test().containerEcho({stringArg}).stdout()
		//   }
		//
		//   @func()
		//   async printDefault(): Promise<string> {
		//     return dag.test().containerEcho().stdout()
		//   }
		// }
		// `,
		//		},
		//		{
		//			sdk: "python",
		//			source: `import dagger
		// from dagger import dag, function, object_type
		//
		// @object_type
		// class Test:
		//     @function
		//     def container_echo(self, string_arg: str = "Hello Self Calls") -> dagger.Container:
		//         return dag.container().from_("alpine:latest").with_exec(["echo", string_arg])
		//
		//     @function
		//     async def print(self, string_arg: str) -> str:
		//         return await dag.test().container_echo(string_arg=string_arg).stdout()
		//
		//     @function
		//     async def print_default(self) -> str:
		//         return await dag.test().container_echo().stdout()
		// `,
		//		},
	}

	for _, tc := range tcs {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := modInit(t, c, tc.sdk, tc.source, "--with-self-calls")

			t.Run("can call with arguments", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					With(daggerQuery(`{print(stringArg:"hello")}`)).
					Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `{"print":"hello\n"}`, out)
			})

			t.Run("can call with optional arguments", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					With(daggerQuery(`{printDefault}`)).
					Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `{"printDefault":"Hello Self Calls\n"}`, out)
			})
		})
	}
}
