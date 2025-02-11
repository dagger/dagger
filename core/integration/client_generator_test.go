package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ClientGeneratorTest struct{}

func TestClientGenerator(t *testing.T) {
	testctx.Run(testCtx, t, ClientGeneratorTest{}, Middleware()...)
}

func (ClientGeneratorTest) TestGenerateAndCallDependencies(ctx context.Context, t *testctx.T) {
	t.Run("use remote dependency", func(ctx context.Context, t *testctx.T) {
		type testCase struct {
			baseImage string
			generator string
			callCmd   []string
			setup     dagger.WithContainerFunc
			postSetup dagger.WithContainerFunc
		}

		testCases := []testCase{
			{
				baseImage: golangImage,
				generator: "go",
				callCmd:   []string{"go", "run", "main.go"},
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"go", "mod", "init", "test.com/test"}).
						WithNewFile("main.go", `package main
		
		import (
			"context"
			"fmt"
		
			"test.com/test/dagger"
		)
		
		func main() {
			ctx := context.Background()
		
			dag := dagger.Connect()
		
			res, err := dag.Hello().Hello(ctx)
			if err != nil {
				panic(err)
			}
		
			fmt.Println("result:", res)
		}
		`,
						)
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr
				},
			},
			{
				baseImage: nodeImage,
				generator: "typescript",
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install", "-g", "tsx@4.15.6"}).
						WithExec([]string{"npm", "init", "-y"}).
						WithExec([]string{"npm", "pkg", "set", "type=module"}).
						WithExec([]string{"npm", "install", "-D", "typescript"}).
						WithNewFile("index.ts", `import { connection } from "@dagger.io/dagger"
import { dag } from "@dagger.io/client"

async function main() {
    await connection(async () => {
      const res = await dag.hello().hello()

      console.log("result:", res)
    })
}

main()
`)
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd:   []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			tc := tc

			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				devEngine := devEngineContainerAsService(devEngineContainer(c))

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithServiceBinding("dev-engine", devEngine).
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dev-engine:1234").
					With(daggerUnprivilegedExec("init")).
					With(daggerUnprivilegedExec("install", "github.com/shykes/hello@2d789671a44c4d559be506a9bc4b71b0ba6e23c9")).
					With(tc.setup).
					With(daggerClientAdd(tc.generator)).
					With(tc.postSetup)

				out, err := moduleSrc.With(daggerUnprivilegedRun(tc.callCmd...)).
					Stdout(ctx)

				require.NoError(t, err)
				require.Equal(t, "result: hello, world!\n", out)
			})
		}
	})

	t.Run("use local dependency", func(ctx context.Context, t *testctx.T) {

		type testCase struct {
			baseImage string
			generator string
			callCmd   []string
			setup     dagger.WithContainerFunc
			postSetup dagger.WithContainerFunc
		}

		testCases := []testCase{
			{
				baseImage: golangImage,
				generator: "go",
				callCmd:   []string{"go", "run", "main.go"},
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"go", "mod", "init", "test.com/test"}).
						WithNewFile("main.go", `package main
		
		import (
			"context"
			"fmt"
		
			"test.com/test/dagger"
		)
		
		func main() {
			ctx := context.Background()
		
			dag := dagger.Connect()
		
			res, err := dag.Test().Hello(ctx)
			if err != nil {
				panic(err)
			}
		
			fmt.Println("result:", res)
		}
		`,
						)
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr
				},
			},
			{
				baseImage: nodeImage,
				generator: "typescript",
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install", "-g", "tsx@4.15.6"}).
						WithExec([]string{"npm", "init", "-y"}).
						WithExec([]string{"npm", "pkg", "set", "type=module"}).
						WithExec([]string{"npm", "install", "-D", "typescript"}).
						WithNewFile("index.ts", `import { connection } from "@dagger.io/dagger"
import { dag } from "@dagger.io/client"

async function main() {
    await connection(async () => {
      const res = await dag.test().hello()

      console.log("result:", res)
    })
}

main()
`)
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd:   []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			tc := tc

			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				devEngine := devEngineContainerAsService(devEngineContainer(c))

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).			
					WithWorkdir("/work/dep").
					With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(sdkSource("go", `package main
		
		type Test struct{}
		
		func (t *Test) Hello() string {
			return "hello"
		}`,
					)).
					WithWorkdir("/work").
					WithServiceBinding("dev-engine", devEngine).
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dev-engine:1234").
					With(daggerUnprivilegedExec("init")).
					With(daggerUnprivilegedExec("install", "./dep")).
					With(tc.setup).
					With(daggerClientAdd(tc.generator)).
					With(tc.postSetup)

				out, err := moduleSrc.With(daggerUnprivilegedRun(tc.callCmd...)).
					Stdout(ctx)

				require.NoError(t, err)
				require.Equal(t, "result: hello\n", out)
			})
		}
	})
}
