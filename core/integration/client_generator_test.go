package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ClientGeneratorTest struct{}

func TestClientGenerator(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ClientGeneratorTest{})
}

func (ClientGeneratorTest) TestGenerateAndCallDependencies(ctx context.Context, t *testctx.T) {
	t.Run("no dependency", func(ctx context.Context, t *testctx.T) {
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

  dag, err := dagger.Connect(ctx)
  if err != nil {
    panic(err)
  }

  res, err := dag.Container().From("alpine:3.20.2").WithExec([]string{"echo", "-n", "hello"}).Stdout(ctx)
  if err != nil {
    panic(err)
  }

  fmt.Println("result:", res)
}`)
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
						WithNewFile("index.ts", `import { connection, dag } from "@dagger.io/client"

async function main() {
    await connection(async () => {
      const res = await dag.container().from("alpine:3.20.2").withExec(["echo", "-n", "hello"]).stdout()

      console.log("result:", res)
    })
}

main()`)
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd: []string{"tsx", "index.ts"},
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
					With(tc.setup).
					With(daggerClientAdd(tc.generator)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerUnprivilegedRun(tc.callCmd...)).
						Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello\n", out)
				})

				t.Run(strings.Join(tc.callCmd, " "), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.WithExec(tc.callCmd).Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello\n", out)
				})
			})
		}
	})

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
		
			dag, err := dagger.Connect(ctx)
      if err != nil {
			  panic(err)
      }
		
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
						WithNewFile("index.ts", `import { connection, dag } from "@dagger.io/client"

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
				callCmd: []string{"tsx", "index.ts"},
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

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerUnprivilegedRun(tc.callCmd...)).
						Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello, world!\n", out)
				})

				t.Run(strings.Join(tc.callCmd, " "), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.WithExec(tc.callCmd).Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello, world!\n", out)
				})
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
		
			dag, err := dagger.Connect(ctx)
      if err != nil {
			  panic(err)
      }
		
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
						WithNewFile("index.ts", `import { connection, dag } from "@dagger.io/client"

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
				callCmd: []string{"tsx", "index.ts"},
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

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerUnprivilegedRun(tc.callCmd...)).
						Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello\n", out)
				})

				t.Run(strings.Join(tc.callCmd, " "), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.WithExec(tc.callCmd).Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello\n", out)
				})
			})
		}
	})

	t.Run("self call module", func(ctx context.Context, t *testctx.T) {
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
						With(daggerExec("init", "--name=test", "--sdk=go", "--source=.dagger")).
						WithNewFile(".dagger/main.go", `package main
		
		type Test struct{}
		
		func (t *Test) Hello() string {
			return "hello"
		}
					`).
						WithExec([]string{"go", "mod", "init", "test.com/test"}).
						WithNewFile("main.go", `package main
		
		import (
			"context"
			"fmt"
		
			"test.com/test/dagger"
		)
		
		func main() {
			ctx := context.Background()
		
			dag, err := dagger.Connect(ctx)
      if err != nil {
			  panic(err)
      }
		
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
						With(daggerExec("init", "--name=test", "--sdk=typescript", "--source=.dagger")).
						WithNewFile(".dagger/src/index.ts", `import { object, func } from '@dagger.io/dagger'

@object()
export class Test {
  @func()
  hello(): string {
    return 'hello'
  }
}
				`).
						WithExec([]string{"npm", "install", "-g", "tsx@4.15.6"}).
						WithExec([]string{"npm", "init", "-y"}).
						WithExec([]string{"npm", "pkg", "set", "type=module"}).
						WithExec([]string{"npm", "install", "-D", "typescript"}).
						WithNewFile("index.ts", `import { connection, dag } from "@dagger.io/client"

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
				callCmd: []string{"tsx", "index.ts"},
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
					With(tc.setup).
					With(daggerClientAdd(tc.generator)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerUnprivilegedRun(tc.callCmd...)).
						Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello\n", out)
				})

				t.Run(strings.Join(tc.callCmd, " "), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.WithExec(tc.callCmd).Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello\n", out)
				})
			})
		}
	})
}

func (ClientGeneratorTest) TestOutputDir(ctx context.Context, t *testctx.T) {
	type testSetup struct {
		baseImage string
		generator string
		outputDir string
		setup     dagger.WithContainerFunc
		callCmd   []string
		postSetup dagger.WithContainerFunc
	}

	goTestSetup := func(outputDir string) testSetup {
		return testSetup{
			baseImage: golangImage,
			generator: "go",
			outputDir: outputDir,
			callCmd:   []string{"go", "run", "main.go"},
			setup: func(ctr *dagger.Container) *dagger.Container {
				return ctr.
					WithExec([]string{"go", "mod", "init", "test.com/test"}).
					WithNewFile("main.go", fmt.Sprintf(`package main
import (
  "context"
  "fmt"

  "test.com/test/%s"
)

func main() {
  ctx := context.Background()

  dag, err := dagger.Connect(ctx)
  if err != nil {
	  panic(err)
  }

  res, err := dag.Container().From("alpine:3.20.2").WithExec([]string{"echo", "-n", "hello"}).Stdout(ctx)
  if err != nil {
	  panic(err)
  }

  fmt.Println("result:", res)
}`, outputDir))
			},
			postSetup: func(ctr *dagger.Container) *dagger.Container {
				return ctr
			},
		}
	}

	tsTestSetup := func(outputDir string) testSetup {
		return testSetup{
			outputDir: outputDir,
			baseImage: nodeImage,
			generator: "typescript",
			setup: func(ctr *dagger.Container) *dagger.Container {
				return ctr.
					WithExec([]string{"npm", "install", "-g", "tsx@4.15.6"}).
					WithExec([]string{"npm", "init", "-y"}).
					WithExec([]string{"npm", "pkg", "set", "type=module"}).
					WithExec([]string{"npm", "install", "-D", "typescript"}).
					WithNewFile("index.ts", `import { connection, dag } from "@dagger.io/client"

async function main() {
  await connection(async () => {
    const res = await dag.container().from("alpine:3.20.2").withExec(["echo", "-n", "hello"]).stdout()

    console.log("result:", res)
  })
}

main()`)
			},
			postSetup: func(ctr *dagger.Container) *dagger.Container {
				return ctr.
					WithExec([]string{"npm", "install"})
			},
			callCmd: []string{"tsx", "index.ts"},
		}
	}

	type testCase struct {
		name      string
		outputDir string
	}

	testCases := []testCase{
		{
			name:      "different output directory",
			outputDir: "generated",
		},
		{
			name:      "nested directory",
			outputDir: "generated/nested/test",
		},
		{
			name:      "hidden directory",
			outputDir: ".generated",
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(fmt.Sprintf("%s (%s)", tc.name, tc.outputDir), func(ctx context.Context, t *testctx.T) {
			for _, ts := range []testSetup{
				goTestSetup(tc.outputDir),
				tsTestSetup(tc.outputDir),
			} {
				t.Run(ts.generator, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					devEngine := devEngineContainerAsService(devEngineContainer(c))

					moduleSrc := c.Container().From(ts.baseImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						WithServiceBinding("dev-engine", devEngine).
						WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
						WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dev-engine:1234").
						With(daggerUnprivilegedExec("init")).
						With(ts.setup).
						With(daggerClientAddAt(ts.generator, ts.outputDir)).
						With(ts.postSetup)

					t.Run(fmt.Sprintf("dagger run %s", strings.Join(ts.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
						out, err := moduleSrc.With(daggerUnprivilegedRun(ts.callCmd...)).
							Stdout(ctx)

						require.NoError(t, err)
						require.Equal(t, "result: hello\n", out)
					})

					t.Run(strings.Join(ts.callCmd, " "), func(ctx context.Context, t *testctx.T) {
						out, err := moduleSrc.WithExec(ts.callCmd).Stdout(ctx)

						require.NoError(t, err)
						require.Equal(t, "result: hello\n", out)
					})
				})
			}
		})
	}

	t.Run("generate in root directory", func(ctx context.Context, t *testctx.T) {
		testCases := []testSetup{
			{
				baseImage: golangImage,
				generator: "go",
				outputDir: ".",
				callCmd:   []string{"go", "run", "."},
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"go", "mod", "init", "test.com/test"}).
						WithNewFile("main.go", `package main
import (
  "context"
  "fmt"
)

func main() {
  ctx := context.Background()

  dag, err := Connect(ctx)
  if err != nil {
	  panic(err)
  }

  res, err := dag.Container().From("alpine:3.20.2").WithExec([]string{"echo", "-n", "hello"}).Stdout(ctx)
  if err != nil {
	  panic(err)
  }

  fmt.Println("result:", res)
}`)
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr
				},
			},
			{
				baseImage: nodeImage,
				generator: "typescript",
				outputDir: ".",
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install", "-g", "tsx@4.15.6"}).
						WithExec([]string{"npm", "init", "-y"}).
						WithExec([]string{"npm", "pkg", "set", "type=module"}).
						WithExec([]string{"npm", "install", "-D", "typescript"}).
						WithNewFile("index.ts", `import { connection, dag } from "@dagger.io/client"

async function main() {
  await connection(async () => {
    const res = await dag.container().from("alpine:3.20.2").withExec(["echo", "-n", "hello"]).stdout()

    console.log("result:", res)
  })
}

main()`)
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd: []string{"tsx", "index.ts"},
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
					With(tc.setup).
					With(daggerClientAddAt(tc.generator, tc.outputDir)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerUnprivilegedRun(tc.callCmd...)).
						Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello\n", out)
				})

				t.Run(strings.Join(tc.callCmd, " "), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.WithExec(tc.callCmd).Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello\n", out)
				})
			})
		}
	})
}

func (ClientGeneratorTest) TestCustomClientGenerator(ctx context.Context, t *testctx.T) {
	type testCase struct {
		generatorSDK    string
		generatorSource string
	}

	testCases := []testCase{
		{
			generatorSDK: "go",
			generatorSource: `package main

import (
	"context"
	"dagger/generator/internal/dagger"
)

type Generator struct{}

func (g *Generator) RequiredClientGenerationFiles() []string{
  return []string{}
}

func (g *Generator) GenerateClient(
  ctx context.Context,
  modSource *dagger.ModuleSource,
  introspectionJSON *dagger.File,
  useLocalSdk bool,
) (*dagger.Directory, error) {
  return dag.Directory().WithNewFile("hello.txt", "hello world"), nil
}`,
		},
		{
			generatorSDK: "typescript",
			generatorSource: `import { dag, Directory, object, func, ModuleSource, File } from "@dagger.io/dagger"

@object()
export class Generator {
  @func()
  requiredClientGenerationFiles(): string[] {
    return []
  }

  @func()
  generateClient(
    modSource: ModuleSource,
    introspectionJSON: File,
    useLocalSdk: boolean,
  ): Directory {
    return dag.directory().withNewFile("hello.txt", "hello world")
  }
}`,
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.generatorSDK, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			moduleSrc := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/generator").
				With(daggerExec("init", "--name=generator", fmt.Sprintf("--sdk=%s", tc.generatorSDK), "--source=.")).
				With(sdkSource(tc.generatorSDK, tc.generatorSource)).
				WithWorkdir("/work").
				With(daggerExec("init")).
				With(daggerClientAdd("./generator"))

			out, err := moduleSrc.File("hello.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello world", out)
		})
	}
}
