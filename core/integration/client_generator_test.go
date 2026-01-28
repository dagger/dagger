package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const defaultGenDir = "./dagger"

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
						With(withGoSetup(`package main
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
}`, defaultGenDir))
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
						With(withTypeScriptSetup(`import { connection, dag } from "./dagger/client.gen.js"

async function main() {
    await connection(async () => {
      const res = await dag.container().from("alpine:3.20.2").withExec(["echo", "-n", "hello"]).stdout()

      console.log("result:", res)
    })
}

main()`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd: []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					With(nonNestedDevEngine(c)).
					With(daggerNonNestedExec("init")).
					With(tc.setup).
					With(daggerClientInstall(tc.generator)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerNonNestedRun(tc.callCmd...)).
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
			baseImage      string
			generator      string
			callCmd        []string
			setup          dagger.WithContainerFunc
			postSetup      dagger.WithContainerFunc
			isolateSetup   dagger.WithContainerFunc
			isolateCallCmd []string
		}

		testCases := []testCase{
			{
				baseImage: golangImage,
				generator: "go",
				callCmd:   []string{"go", "run", "main.go"},
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						With(withGoSetup(`package main

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
		`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr
				},
				isolateSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"go", "build", "-o", "/bin/test"}).
						WithWorkdir("/bin")
				},
				isolateCallCmd: []string{"./test"},
			},
			{
				baseImage: nodeImage,
				generator: "typescript",
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						With(withTypeScriptSetup(`import { connection, dag } from "@my-app/dagger"

async function main() {
    await connection(async () => {
      const res = await dag.hello().hello()

      console.log("result:", res)
    })
}

main()
`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd: []string{"tsx", "index.ts"},
				isolateSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithoutFile("dagger.json")
				},
				isolateCallCmd: []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					With(nonNestedDevEngine(c)).
					With(daggerNonNestedExec("init")).
					With(daggerNonNestedExec("install", "github.com/shykes/hello@2d789671a44c4d559be506a9bc4b71b0ba6e23c9")).
					With(tc.setup).
					With(daggerClientInstall(tc.generator)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerNonNestedRun(tc.callCmd...)).
						Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello, world!\n", out)
				})

				t.Run(strings.Join(tc.callCmd, " "), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.WithExec(tc.callCmd).Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello, world!\n", out)
				})

				t.Run(fmt.Sprintf("isolated dagger run %s", strings.Join(tc.isolateCallCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.
						With(tc.isolateSetup).
						With(daggerNonNestedRun(tc.isolateCallCmd...)).
						Stdout(ctx)

					require.NoError(t, err)
					require.Equal(t, "result: hello, world!\n", out)
				})

				t.Run(fmt.Sprintf("isolated %s", strings.Join(tc.isolateCallCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.
						With(tc.isolateSetup).
						WithExec(tc.isolateCallCmd).
						Stdout(ctx)

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
						With(withGoSetup(`package main

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
		`, defaultGenDir))
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
						With(withTypeScriptSetup(`import { connection, dag } from "@my-app/dagger"

async function main() {
    await connection(async () => {
      const res = await dag.test().hello()

      console.log("result:", res)
    })
}

main()
`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd: []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

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
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					With(nonNestedDevEngine(c)).
					With(daggerNonNestedExec("init")).
					With(daggerNonNestedExec("install", "./dep")).
					With(tc.setup).
					With(daggerClientInstall(tc.generator)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerNonNestedRun(tc.callCmd...)).
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

import "context"

type Test struct{}

func (t *Test) Hello(ctx context.Context) (string, error) {
	return dag.Container().From("alpine:3.20.2").WithExec([]string{"echo", "-n", "hello"}).Stdout(ctx)
}
					`).
						With(withGoSetup(`package main

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
		`, defaultGenDir))
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
						WithNewFile(".dagger/src/index.ts", `import { dag, object, func } from '@dagger.io/dagger'

@object()
export class Test {
  @func()
  async hello(): Promise<string> {
    return dag.container().from("alpine:3.20.2").withExec(["echo", "-n", "hello"]).stdout()
  }
}
				`).
						With(withTypeScriptSetup(`import { connection, dag } from "@my-app/dagger"

async function main() {
  await connection(async () => {
    const res = await dag.test().hello()

    console.log("result:", res)
  })
}

main()
`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd: []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					With(nonNestedDevEngine(c)).
					With(tc.setup).
					With(daggerClientInstall(tc.generator)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerNonNestedRun(tc.callCmd...)).
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

func (ClientGeneratorTest) TestPersistence(ctx context.Context, t *testctx.T) {
	t.Run("work without a module implementation", func(ctx context.Context, t *testctx.T) {
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
						With(withGoSetup(`package main

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
		`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithoutDirectory("dagger")
				},
			},
			{
				baseImage: nodeImage,
				generator: "typescript",
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						With(withTypeScriptSetup(`import { connection, dag } from "@my-app/dagger"

async function main() {
    await connection(async () => {
      const res = await dag.hello().hello()

      console.log("result:", res)
    })
}

main()
`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"}).
						WithoutDirectory("dagger")
				},
				callCmd: []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					With(nonNestedDevEngine(c)).
					With(daggerNonNestedExec("init")).
					With(daggerNonNestedExec("install", "github.com/shykes/hello@2d789671a44c4d559be506a9bc4b71b0ba6e23c9")).
					With(tc.setup).
					With(daggerClientInstall(tc.generator)).
					With(tc.postSetup)

				modCfgContents, err := moduleSrc.
					File("dagger.json").
					Contents(ctx)
				require.NoError(t, err)

				var modCfg modules.ModuleConfig
				require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
				require.Equal(t, 1, len(modCfg.Clients))
				require.Equal(t, tc.generator, modCfg.Clients[0].Generator)
				require.Equal(t, "dagger", modCfg.Clients[0].Directory)

				// Execute module after regeneration
				out, err := moduleSrc.
					With(daggerNonNestedExec("develop")).
					With(daggerNonNestedRun(tc.callCmd...)).
					Stdout(ctx)

				require.NoError(t, err)
				require.Equal(t, "result: hello, world!\n", out)
			})
		}
	})

	t.Run("cohexist with a module implementation", func(ctx context.Context, t *testctx.T) {
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
						With(daggerNonNestedExec("init", "--name=test", "--sdk=go", "--source=.dagger")).
						WithNewFile(".dagger/main.go", `package main

			type Test struct{}

			func (t *Test) Hello() string {
				return "hello"
			}
						`).
						With(withGoSetup(`package main

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
			`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					// Remove generated files so they can be regenerated using dagger develop
					return ctr.WithoutDirectory("dagger").WithoutFile(".dagger/dagger.gen.go")
				},
			},
			{
				baseImage: nodeImage,
				generator: "typescript",
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						With(daggerNonNestedExec("init", "--name=test", "--sdk=typescript", "--source=.dagger")).
						WithNewFile(".dagger/src/index.ts", `import { object, func } from '@dagger.io/dagger'

@object()
export class Test {
  @func()
  hello(): string {
    return 'hello'
  }
}`).
						With(withTypeScriptSetup(`import { connection, dag } from "@my-app/dagger"

async function main() {
  await connection(async () => {
    const res = await dag.test().hello()

    console.log("result:", res)
  })
}

main()
		`, defaultGenDir))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					// Remove generated files so they can be regenerated using dagger develop
					return ctr.
						WithExec([]string{"npm", "install"}).
						WithoutDirectory("dagger").
						WithoutDirectory(".dagger/sdk")
				},
				callCmd: []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					With(nonNestedDevEngine(c)).
					With(tc.setup).
					With(daggerClientInstall(tc.generator)).
					With(tc.postSetup)

				modCfgContents, err := moduleSrc.
					File("dagger.json").
					Contents(ctx)
				require.NoError(t, err)

				var modCfg modules.ModuleConfig
				require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
				require.Equal(t, 1, len(modCfg.Clients))
				require.Equal(t, tc.generator, modCfg.Clients[0].Generator)
				require.Equal(t, "dagger", modCfg.Clients[0].Directory)

				// Execute module after regeneration
				out, err := moduleSrc.
					With(daggerNonNestedExec("develop")).
					With(daggerNonNestedRun(tc.callCmd...)).
					Stdout(ctx)

				require.NoError(t, err)
				require.Equal(t, "result: hello\n", out)
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
					With(withGoSetup(fmt.Sprintf(`package main
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
}`, outputDir), "./"+outputDir))
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
					With(withTypeScriptSetup(`import { connection, dag } from "@my-app/dagger"

async function main() {
  await connection(async () => {
    const res = await dag.container().from("alpine:3.20.2").withExec(["echo", "-n", "hello"]).stdout()

    console.log("result:", res)
  })
}

main()`, "./"+outputDir))
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
		t.Run(fmt.Sprintf("%s %q", tc.name, tc.outputDir), func(ctx context.Context, t *testctx.T) {
			for _, ts := range []testSetup{
				goTestSetup(tc.outputDir),
				tsTestSetup(tc.outputDir),
			} {
				t.Run(ts.generator, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					moduleSrc := c.Container().From(ts.baseImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
						With(nonNestedDevEngine(c)).
						With(daggerNonNestedExec("init")).
						With(ts.setup).
						With(daggerClientInstallAt(ts.generator, ts.outputDir)).
						With(ts.postSetup)

					t.Run(fmt.Sprintf("dagger run %s", strings.Join(ts.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
						out, err := moduleSrc.With(daggerNonNestedRun(ts.callCmd...)).
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
						With(withGoSetup(`package main
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
}`, "."))
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
						With(withTypeScriptSetup(`import { connection, dag } from "@my-app/dagger"

async function main() {
  await connection(async () => {
    const res = await dag.container().from("alpine:3.20.2").withExec(["echo", "-n", "hello"]).stdout()

    console.log("result:", res)
  })
}

main()`, "."))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd: []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					With(nonNestedDevEngine(c)).
					With(daggerNonNestedExec("init")).
					With(tc.setup).
					With(daggerClientInstallAt(tc.generator, tc.outputDir)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerNonNestedRun(tc.callCmd...)).
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

	t.Run("generate in root directory different from context directory", func(ctx context.Context, t *testctx.T) {
		testCases := []testSetup{
			{
				baseImage: golangImage,
				generator: "go",
				outputDir: ".",
				callCmd:   []string{"go", "run", "."},
				setup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						With(withGoSetup(`package main
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
}`, "."))
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
						With(withTypeScriptSetup(`import { connection, dag } from "@my-app/dagger"

async function main() {
  await connection(async () => {
    const res = await dag.container().from("alpine:3.20.2").withExec(["echo", "-n", "hello"]).stdout()

    console.log("result:", res)
  })
}

main()`, "."))
				},
				postSetup: func(ctr *dagger.Container) *dagger.Container {
					return ctr.
						WithExec([]string{"npm", "install"})
				},
				callCmd: []string{"tsx", "index.ts"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				moduleSrc := c.Container().From(tc.baseImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					// Init the repo here so the context is a parent dir of the root dir
					WithExec([]string{"apk", "add", "git"}).
					WithExec([]string{"git", "init"}).
					WithWorkdir("/work/dir/dir").
					WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
					With(nonNestedDevEngine(c)).
					With(daggerNonNestedExec("init")).
					With(tc.setup).
					With(daggerClientInstallAt(tc.generator, tc.outputDir)).
					With(tc.postSetup)

				t.Run(fmt.Sprintf("dagger run %s", strings.Join(tc.callCmd, " ")), func(ctx context.Context, t *testctx.T) {
					out, err := moduleSrc.With(daggerNonNestedRun(tc.callCmd...)).
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
			// Omit `dev` from signature to verify that it works if it's not defined.
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
) (*dagger.Directory, error) {
  return dag.Directory().WithNewFile("hello.txt", "hello world"), nil
}`,
		},
		{
			generatorSDK: "typescript",
			// Omit `dev` from signature to verify that it works if it's not defined.
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
  ): Directory {
    return dag.directory().withNewFile("hello.txt", "hello world")
  }
}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.generatorSDK, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			moduleSrc := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/generator").
				With(daggerExec("init", "--name=generator", fmt.Sprintf("--sdk=%s", tc.generatorSDK), "--source=.")).
				With(sdkSource(tc.generatorSDK, tc.generatorSource)).
				WithWorkdir("/work").
				With(daggerExec("init")).
				With(daggerExec("client", "install", "./generator"))

			out, err := moduleSrc.File("hello.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello world", out)
		})
	}
}

func (ClientGeneratorTest) TestMultipleClient(ctx context.Context, t *testctx.T) {
	t.Run("go", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		moduleSrc := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
			With(nonNestedDevEngine(c)).
			With(daggerNonNestedExec("init")).
			WithExec([]string{"go", "mod", "init", "test.com/test"}).
			WithExec([]string{"go", "mod", "edit", "-replace", "dagger.io/dagger=./dagger/sdk"}).
			// Install both client
			With(daggerClientInstallAt("go", "client1")).
			With(daggerClientInstallAt("go", "client2")).
			WithNewFile("main.go", `package main

import (
  "context"
  "fmt"

  c1 "test.com/test/client1"
  c2 "test.com/test/client2"
)

func main() {
  ctx := context.Background()

  dag1, err := c1.Connect(ctx)
  if err != nil {
    panic(err)
  }

  res, err := dag1.Container().From("alpine:3.20.2").WithExec([]string{"echo", "-n", "hello"}).Stdout(ctx)
  if err != nil {
    panic(err)
  }

  fmt.Println("result 1:", res)

  dag2, err := c2.Connect(ctx)
  if err != nil {
    panic(err)
  }

  res2, err := dag2.Container().From("alpine:3.20.2").WithExec([]string{"echo", "-n", "hello"}).Stdout(ctx)
  if err != nil {
    panic(err)
  }

  fmt.Println("result 2:", res2)
}
`)

		t.Run("dagger run go run main.go", func(ctx context.Context, t *testctx.T) {
			out, err := moduleSrc.With(daggerNonNestedRun("go", "run", "main.go")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "result 1: hello\nresult 2: hello\n", out)
		})

		t.Run("go run main.go", func(ctx context.Context, t *testctx.T) {
			out, err := moduleSrc.WithExec([]string{"go", "run", "main.go"}).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "result 1: hello\nresult 2: hello\n", out)
		})
	})

	t.Run("typescript", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		moduleSrc := c.Container().From(nodeImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
			With(nonNestedDevEngine(c)).
			With(daggerNonNestedExec("init")).
			WithExec([]string{"npm", "install", "-g", "tsx@4.15.6"}).
			WithExec([]string{"npm", "init", "-y"}).
			WithExec([]string{"npm", "pkg", "set", "type=module"}).
			WithExec([]string{"npm", "install", "-D", "typescript"}).
			WithNewFile("index.ts", `import { connection as c1, dag as dag1 } from "@my-app/dagger1";
import { connection as c2, dag as dag2 } from "@my-app/dagger2";

async function main() {
  await c1(async () => {
    const res = await dag1
      .container()
      .from("alpine:3.20.2")
      .withExec(["echo", "-n", "hello"])
      .stdout();

    console.log("result 1:", res);
  });

  await c2(async () => {
    const res = await dag2
      .container()
      .from("alpine:3.20.2")
      .withExec(["echo", "-n", "hello"])
      .stdout();

    console.log("result 2:", res);
  });
}

main();
`).
			WithNewFile("tsconfig.json", `{
  "compilerOptions": {
    "paths": {
      "@my-app/dagger1": ["./dagger1/client.gen.ts"],
      "@my-app/dagger2": ["./dagger2/client.gen.ts"]
    }
  }
}`).
			With(daggerClientInstallAt("typescript", "dagger1")).
			With(daggerClientInstallAt("typescript", "dagger2")).
			WithExec([]string{"npm", "install"})

		t.Run("dagger run tsx index.ts", func(ctx context.Context, t *testctx.T) {
			out, err := moduleSrc.With(daggerNonNestedRun("tsx", "index.ts")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "result 1: hello\nresult 2: hello\n", out)
		})

		t.Run("tsx index.ts", func(ctx context.Context, t *testctx.T) {
			out, err := moduleSrc.WithExec([]string{"tsx", "index.ts"}).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "result 1: hello\nresult 2: hello\n", out)
		})
	})
}

func (ClientGeneratorTest) TestGlobalClient(ctx context.Context, t *testctx.T) {
	t.Run("go", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		moduleSrc := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
			With(nonNestedDevEngine(c)).
			With(daggerNonNestedExec("init")).
			With(daggerNonNestedExec("install", "github.com/shykes/hello@2d789671a44c4d559be506a9bc4b71b0ba6e23c9")).
			With(withGoSetup(`package main
import (
  "context"
  "fmt"

  "test.com/test/dagger/dag"
)

func main() {
  ctx := context.Background()

  res, err := dag.Container().From("alpine:3.20.2").WithExec([]string{"echo", "-n", "hello"}).Stdout(ctx)
  if err != nil {
    panic(err)
  }

  fmt.Println("result:", res)
}`, defaultGenDir)).
			With(daggerClientInstall("go"))

		t.Run("dagger run go run .", func(ctx context.Context, t *testctx.T) {
			out, err := moduleSrc.With(daggerNonNestedRun("go", "run", ".")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "result: hello\n")
		})

		t.Run("go run .", func(ctx context.Context, t *testctx.T) {
			out, err := moduleSrc.WithExec([]string{"go", "run", "."}).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "result: hello\n")
		})
	})
}

func (ClientGeneratorTest) TestClientCommands(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	mainGoFile := `package main
import (
  "context"
  "fmt"

  dagger "test.com/test/dagger"
	dagger2 "test.com/test/dagger2"
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

	dag2, err := dagger2.Connect(ctx)
	if err != nil {
		panic(err)
	}

	res2, err := dag2.Hello().Hello(ctx)
	if err != nil {
		panic(err)
	}

  fmt.Println("result dag1:", res)
	fmt.Println("result dag2:", res2)
}`

	moduleSrc := c.Container().
		From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
		With(nonNestedDevEngine(c)).
		With(daggerNonNestedExec("init")).
		With(daggerNonNestedExec("install", "github.com/shykes/hello@2d789671a44c4d559be506a9bc4b71b0ba6e23c9")).
		WithExec([]string{"go", "mod", "init", "test.com/test"}).
		WithExec([]string{"go", "mod", "edit", "-replace", "dagger.io/dagger=./dagger/sdk"}).
		// We cannot directly import both clients because path will not be
		// recognized during post client operation like go mod tidy.
		WithNewFile("main.go", `package main`).
		With(daggerClientInstallAt("go", "./dagger")).
		With(daggerClientInstallAt("go", "./dagger2")).
		WithNewFile("main.go", mainGoFile)

	t.Run("execute two differents clients in one session", func(ctx context.Context, t *testctx.T) {
		out, err := moduleSrc.With(daggerNonNestedRun("go", "run", "main.go")).Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "result dag1: hello, world!\nresult dag2: hello, world!\n", out)
	})

	t.Run("list clients", func(ctx context.Context, t *testctx.T) {
		out, err := moduleSrc.WithExec([]string{"dagger", "client", "list", "--json"}).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `[{"Generator":"go","Directory":"./dagger"},{"Generator":"go","Directory":"./dagger2"}]`, out)
	})

	t.Run("uninstall client", func(ctx context.Context, t *testctx.T) {
		ctr := moduleSrc.WithExec([]string{"dagger", "client", "uninstall", "dagger"})

		out, err := ctr.Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "Client at dagger removed from config.\n")

		out, err = ctr.WithExec([]string{"dagger", "client", "list", "--json"}).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `[{"Generator":"go","Directory":"./dagger2"}]`, out)
	})
}

func (ClientGeneratorTest) TestClientUpdate(ctx context.Context, t *testctx.T) {
	const (
		// pins from github.com/dagger/client-generator-test module
		// to test the update command
		latestPin = "d76af75d104a2777bcd7dc6d240966760801b81f"
		v01Pin    = "446f2691deba58f99b55b86430fade1c773e486f"
	)

	// dagger.json files to use for initial setup
	noClient := `{
	"name": "test",
	"clients": []
}`

	clientwithOldVersion := `{
	"name": "test",
	"clients": [
		{
			"generator": "github.com/dagger/client-generator-test@` + v01Pin + `",
			"directory": "dagger"
		}
	]
}`

	clientwithBranch := `{
	"name": "test",
	"clients": [
		{
			"generator": "github.com/dagger/client-generator-test@main",
			"directory": "dagger"
		}
	]
}`

	clientWithNoVersion := `{
	"name": "test",
	"clients": [
		{
			"generator": "github.com/dagger/client-generator-test",
			"directory": "dagger"
		}
	]
}`

	type testCase struct {
		name          string
		daggerjson    string
		updateCmd     []string
		contains      []string
		notContains   []string
		expectedError string
	}

	testCases := []testCase{
		{
			name:        "existing client has version, update cmd has version",
			daggerjson:  clientwithOldVersion,
			updateCmd:   []string{"client", "update", "github.com/dagger/client-generator-test@v0.0.2"},
			contains:    []string{"github.com/dagger/client-generator-test@v0.0.2"},
			notContains: []string{fmt.Sprintf("github.com/dagger/client-generator-test@%s", v01Pin)},
		},
		{
			name:        "existing client has branch, update cmd has version",
			daggerjson:  clientwithBranch,
			updateCmd:   []string{"client", "update", "github.com/dagger/client-generator-test@v0.0.2"},
			contains:    []string{"github.com/dagger/client-generator-test@v0.0.2"},
			notContains: []string{"github.com/dagger/client-generator-test@main"},
		},
		{
			name:        "existing client dont have version, update cmd has version",
			daggerjson:  clientWithNoVersion,
			updateCmd:   []string{"client", "update", "github.com/dagger/client-generator-test@v0.0.2"},
			contains:    []string{"github.com/dagger/client-generator-test@v0.0.2"},
			notContains: []string{"github.com/dagger/client-generator-test\""},
		},
		{
			name:       "existing client has version, update cmd has no version",
			daggerjson: clientwithOldVersion,
			updateCmd:  []string{"client", "update", "github.com/dagger/client-generator-test"},
			contains:   []string{fmt.Sprintf("github.com/dagger/client-generator-test@%s", latestPin)},
			notContains: []string{
				"github.com/dagger/client-generator-test@v0.0.2",
				"github.com/dagger/client-generator-test@main",
			},
		},
		{
			name:       "existing client has no version, update cmd has no version",
			daggerjson: clientWithNoVersion,
			updateCmd:  []string{"client", "update", "github.com/dagger/client-generator-test"},
			contains:   []string{fmt.Sprintf("github.com/dagger/client-generator-test@%s", latestPin)},
			notContains: []string{
				"github.com/dagger/client-generator-test\"",
			},
		},
		{
			name:       "existing client has branch, update cmd has no version",
			daggerjson: clientwithBranch,
			updateCmd:  []string{"client", "update", "github.com/dagger/client-generator-test"},
			contains:   []string{fmt.Sprintf("github.com/dagger/client-generator-test@%s", latestPin)},
			notContains: []string{
				"github.com/dagger/client-generator-test@main",
			},
		},
		{
			name:       "existing client has version, update cmd has branch",
			daggerjson: clientwithOldVersion,
			updateCmd:  []string{"client", "update", "github.com/dagger/client-generator-test@main"},
			contains:   []string{"github.com/dagger/client-generator-test@main"},
			notContains: []string{
				fmt.Sprintf("github.com/dagger/client-generator-test@%s", v01Pin),
			},
		},
		{
			name:       "existing client has no version, update cmd has branch",
			daggerjson: clientWithNoVersion,
			updateCmd:  []string{"client", "update", "github.com/dagger/client-generator-test@main"},
			contains:   []string{"github.com/dagger/client-generator-test@main"},
			notContains: []string{
				"github.com/dagger/client-generator-test\"",
			},
		},
		{
			name:          "update a client not in the dagger.json",
			daggerjson:    noClient,
			updateCmd:     []string{"client", "update", "github.com/dagger/client-generator-test@v0.0.2"},
			expectedError: `client(s) "github.com/dagger/client-generator-test" were requested to be updated, but were not found in the clients list`,
		},
		{
			name:       "can update all clients",
			daggerjson: clientwithOldVersion,
			updateCmd:  []string{"client", "update"},
			contains:   []string{fmt.Sprintf("github.com/dagger/client-generator-test@%s", latestPin)},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modCtr := c.Container().From("alpine:3.20.2").
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init")).
				WithNewFile("/work/dagger.json", tc.daggerjson)

			daggerjson, err := modCtr.
				With(daggerExec(tc.updateCmd...)).
				File("dagger.json").
				Contents(ctx)

			if tc.expectedError != "" {
				requireErrOut(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)

				for _, s := range tc.contains {
					require.Contains(t, daggerjson, s)
				}

				for _, s := range tc.notContains {
					require.NotContains(t, daggerjson, s)
				}
			}
		})
	}
}

func (ClientGeneratorTest) TestHostCall(ctx context.Context, t *testctx.T) {
	type testCase struct {
		baseImage string
		generator string
		callCmd   []string
		setup     dagger.WithContainerFunc
		postSetup dagger.WithContainerFunc
		expected  string
	}

	testCases := []testCase{
		{
			baseImage: golangImage,
			generator: "go",
			callCmd:   []string{"go", "run", "main.go"},
			setup: func(ctr *dagger.Container) *dagger.Container {
				return ctr.
					With(withGoSetup(`package main

import (
	"context"
	"fmt"

	"test.com/test/dagger/dag"
)

func main() {
	ctx := context.Background()
	result, err := dag.Host().Directory("files").Entries(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println(result)
}`, defaultGenDir))
			},
			postSetup: func(ctr *dagger.Container) *dagger.Container {
				return ctr
			},
			expected: "[file1.txt]\n",
		},
		{
			baseImage: nodeImage,
			generator: "typescript",
			setup: func(ctr *dagger.Container) *dagger.Container {
				return ctr.
					With(withTypeScriptSetup(`import { dag, connection } from "@my-app/dagger"

async function main() {
  await connection(async () => {
    const result = await dag.host().directory("files").entries()
    console.log(result)
  })
}

main()`, defaultGenDir))
			},
			postSetup: func(ctr *dagger.Container) *dagger.Container {
				return ctr.
					WithExec([]string{"npm", "install"})
			},
			callCmd:  []string{"tsx", "index.ts"},
			expected: "[ 'file1.txt' ]\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.generator, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			moduleSrc := c.Container().From(tc.baseImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
				With(nonNestedDevEngine(c)).
				With(daggerNonNestedExec("init")).
				With(tc.setup).
				With(daggerClientInstall(tc.generator)).
				With(tc.postSetup).
				WithDirectory("files", c.Directory().WithNewFile("file1.txt", "hello world"))

			out, err := moduleSrc.With(daggerNonNestedRun(tc.callCmd...)).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, tc.expected, out)
		})
	}
}

func (ClientGeneratorTest) TestMissmatchDependencyVersion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	moduleSrc := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
		With(nonNestedDevEngine(c)).
		With(daggerNonNestedExec("init")).
		With(daggerNonNestedExec("install", "github.com/shykes/hello@2d789671a44c4d559be506a9bc4b71b0ba6e23c9")).
		With(withGoSetup(`package main

		import (
			"context"
			"fmt"
			"os"

			"test.com/test/dagger"
		)

		func main() {
			ctx := context.Background()

			dag, err := dagger.Connect(ctx)
      if err != nil {
			  fmt.Println(err)
				os.Exit(0)
      }

			res, err := dag.Hello().Hello(ctx)
			if err != nil {
				panic(err)
			}

			fmt.Println("result:", res)
		}
		`,
			defaultGenDir)).
		With(daggerClientInstall("go")).
		WithExec([]string{"apk", "add", "jq"}).
		// Update the dagger.json manually to not rettrigger the generation so we can verify that it triggers an error
		// on execute
		WithExec([]string{"sh", "-c", `sh -c 'cat dagger.json | jq '\''(.dependencies[] | select(.name == "hello") | .source) = "github.com/shykes/hello@main" | (.dependencies[] | select(.name == "hello") | .pin) = "2d789671a44c4d559be506a9bc4b71b0ba6e23c9"'\'' > dagger.tmp && mv dagger.tmp dagger.json'`})

	out, err := moduleSrc.With(daggerNonNestedRun("go", "run", "main.go")).Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, out, "error serving dependency hello: module hello")
}

func (ClientGeneratorTest) TestNoGoProjectSetup(ctx context.Context, t *testctx.T) {
	t.Run("add main after generating the client on an empty directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modCtr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
			With(nonNestedDevEngine(c)).
			With(daggerNonNestedExec("init", "--name=test")).
			With(daggerClientInstall("go"))

		// Verify that we generated the go.mod and the library in the default location
		generatedFiles, err := modCtr.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, generatedFiles, "dagger.json")
		require.Contains(t, generatedFiles, "go.mod")
		require.Contains(t, generatedFiles, "dagger/")

		// Add a main.go file to use the generated client.
		modCtr = modCtr.WithNewFile("main.go", `package main
import (
	"context"
	"fmt"

	"test/dagger/dag"
)

func main() {
	ctx := context.Background()

	res, err := dag.Container().From("alpine:3.20.2").WithExec([]string{"echo", "hello"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println("result:", res)
}
`)

		out, err := modCtr.With(daggerNonNestedRun("go", "run", "main.go")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "result: hello\n")
	})

	t.Run("generate client as a sub module of an existing go project", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modCtr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
			With(nonNestedDevEngine(c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "test"}).
			WithWorkdir("/work/lib").
			With(daggerNonNestedExec("init", "--name=testlib")).
			With(daggerClientInstall("go")).
			WithWorkdir("/work").
			// Add the generated client to the parent go.mod
			WithExec([]string{"go", "mod", "edit", "-require", "testlib@v0.0.0"}).
			WithExec([]string{"go", "mod", "edit", "-replace", "testlib=./lib"}).
			WithNewFile("main.go", `package main
import (
	"context"
	"fmt"

	"testlib/dagger/dag"
)

func main() {
	ctx := context.Background()

	res, err := dag.Container().From("alpine:3.20.2").WithExec([]string{"echo", "hello"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println("result:", res)
}`).
			WithExec([]string{"go", "mod", "tidy"})

		out, err := modCtr.With(daggerNonNestedRun("go", "run", "main.go")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "result: hello\n")
	})

	t.Run("generate a client on a go project with only a go.mod file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modCtr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", "/bin/dagger").
			With(nonNestedDevEngine(c)).
			WithExec([]string{"go", "mod", "init", "test.com/test"}).
			With(daggerNonNestedExec("init", "--name=test")).
			With(daggerClientInstall("go")).
			WithNewFile("main.go", `package main
import (
	"context"
	"fmt"

	"test.com/test/dagger/dag"
)

func main() {
	ctx := context.Background()

	res, err := dag.Container().From("alpine:3.20.2").WithExec([]string{"echo", "hello"}).Stdout(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Println("result:", res)
}
`)

		out, err := modCtr.With(daggerNonNestedRun("go", "run", "main.go")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "result: hello\n")
	})
}

func withGoSetup(content string, outputDir string) func(*dagger.Container) *dagger.Container {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithExec([]string{"go", "mod", "init", "test.com/test"}).
			WithExec([]string{"go", "mod", "edit", "-replace", fmt.Sprintf("dagger.io/dagger=%s/sdk", outputDir)}).
			WithNewFile("main.go", content)
	}
}

func withTypeScriptSetup(content string, outputDir string) func(*dagger.Container) *dagger.Container {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithExec([]string{"npm", "install", "-g", "tsx@4.15.6"}).
			WithExec([]string{"npm", "init", "-y"}).
			WithExec([]string{"npm", "pkg", "set", "type=module"}).
			WithExec([]string{"npm", "install", "-D", "typescript"}).
			WithNewFile("index.ts", content).
			WithNewFile("tsconfig.json", fmt.Sprintf(`{
  "compilerOptions": {
    "paths": {
      "@my-app/dagger": ["%s/client.gen.ts"]
    }
  }
}`, outputDir))
	}
}
