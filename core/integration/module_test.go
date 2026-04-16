package core

// Workspace alignment: partially aligned; this historical umbrella suite still needs further cleanup.
// Scope: Remaining broad historical module coverage around runtime behavior, context handling, current-module APIs, and legacy authoring flows not yet split into narrower suites.
// Intent: Preserve confidence while incrementally extracting clearer module-owned suites out of the historical umbrella.
//
// Cleanup plan:
// 1. Done: exact-by-intent helpers live in module_helpers_test.go.
// 2. Done: legacy rewrite helpers live in module_legacy_helpers_test.go, visibly quarantined.
// 3. Done: workspace-owned command helpers live in workspace_test.go.
// 4. Next: peel additional coherent coverage slices out of this file without changing behavior.

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
)

type ModuleSuite struct{}

func TestModule(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleSuite{})
}

func (ModuleSuite) TestSelfAPICall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
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

func (ModuleSuite) TestExecError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=playground", "--sdk=go")).
		WithNewFile("main.go", `
package main

import (
	"context"
	"errors"
)

type Playground struct{}

func (p *Playground) DoThing(ctx context.Context) error {
	_, err := dag.Container().From("`+alpineImage+`").WithExec([]string{"sh", "-c", "exit 5"}).Sync(ctx)
	var e *ExecError
	if errors.As(err, &e) {
		if e.ExitCode == 5 {
			return nil
		}
	}
	panic("yikes")
}
`,
		)

	_, err := modGen.
		With(daggerQuery(`{doThing}`)).
		Stdout(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestCurrentModuleAPI(ctx context.Context, t *testctx.T) {
	t.Run("generatedContextDirectory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", `package main

			import "context"
			import "dagger/test/internal/dagger"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) *dagger.Directory {
				return dag.CurrentModule().GeneratedContextDirectory()
			}
			`,
			).
			With(daggerCall("fn", "export", "--path=./out")).
			Directory("out").
			Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "dagger.gen.go")
	})

	t.Run("dependencies", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "depA")).
			With(daggerExec("init", "--sdk=go", "depB")).
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "./depA")).
			With(daggerExec("install", "./depB")).
			WithNewFile("/work/main.go", `package main

			import "context"
			import "sort"
			import "strings"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) {
				deps, err := dag.CurrentModule().Dependencies(ctx)
				if err != nil {
					return "", err
				}

				var depNames []string
				for _, dep := range deps {
					depName, err := dep.Name(ctx)
					if err != nil {
						return "", err
					}

					depNames = append(depNames, depName)
				}

				sort.Strings(depNames)

				return strings.Join(depNames, ","), nil
			}
			`,
			).
			With(daggerCall("fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out, "depA,depB")
	})

	t.Run("name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=WaCkY", "--sdk=go")).
			WithNewFile("/work/main.go", `package main

			import "context"

			type WaCkY struct {}

			func (m *WaCkY) Fn(ctx context.Context) (string, error) {
				return dag.CurrentModule().Name(ctx)
			}
			`,
			).
			With(daggerCall("fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "WaCkY", strings.TrimSpace(out))
	})

	t.Run("source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/subdir/coolfile.txt", "nice").
			WithNewFile("/work/main.go", `package main

			import (
				"context"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) *dagger.File {
				return dag.CurrentModule().Source().File("subdir/coolfile.txt")
			}
			`,
			).
			With(daggerCall("fn", "contents")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "nice", strings.TrimSpace(out))
	})

	t.Run("workdir", func(ctx context.Context, t *testctx.T) {
		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (*dagger.Directory, error) {
				if err := os.MkdirAll("subdir/moresubdir", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0644); err != nil {
					return nil, err
				}
				return dag.CurrentModule().Workdir("subdir/moresubdir"), nil
			}
			`,
				).
				With(daggerCall("fn", "file", "--path=coolfile.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (*dagger.File, error) {
				if err := os.MkdirAll("subdir/moresubdir", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0644); err != nil {
					return nil, err
				}
				return dag.CurrentModule().WorkdirFile("subdir/moresubdir/coolfile.txt"), nil
			}
			`,
				).
				With(daggerCall("fn", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("error on escape", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			func New() (*Test, error) {
				if err := os.WriteFile("/rootfile.txt", []byte("notnice"), 0644); err != nil {
					return nil, err
				}
				if err := os.MkdirAll("/foo", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("/foo/foofile.txt", []byte("notnice"), 0644); err != nil {
					return nil, err
				}

				return &Test{}, nil
			}

			type Test struct {}

			func (m *Test) EscapeFile(ctx context.Context) *dagger.File {
				return dag.CurrentModule().WorkdirFile("../rootfile.txt")
			}

			func (m *Test) EscapeFileAbs(ctx context.Context) *dagger.File {
				return dag.CurrentModule().WorkdirFile("/rootfile.txt")
			}

			func (m *Test) EscapeDir(ctx context.Context) *dagger.Directory {
				return dag.CurrentModule().Workdir("../foo")
			}

			func (m *Test) EscapeDirAbs(ctx context.Context) *dagger.Directory {
				return dag.CurrentModule().Workdir("/foo")
			}
			`,
				)

			_, err := ctr.
				With(daggerCall("escape-file", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-file-abs", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../foo" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir-abs", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/foo" escapes workdir`)
		})
	})
}

func (ModuleSuite) TestCustomSDK(ctx context.Context, t *testctx.T) {
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/coolsdk").
			With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"encoding/json"

	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct {}

func (m *CoolSdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
	mod := modSource.WithSDK("go").AsModule()
	modID, err := mod.ID(ctx)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(modID)
	if err != nil {
		return nil, err
	}
	return dag.Container().
		From("alpine").
		WithNewFile(outputFilePath, string(b)).
		WithEntrypoint([]string{
			"sh", "-c", "",
		}), nil
}

func (m *CoolSdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
			WithNewFile("main.go", `package main

import "os"

type Test struct {}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
`,
			)

		out, err := ctr.
			With(daggerCall("fn")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(out))
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("git", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			ctr := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(privateSetup).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk="+testGitModuleRef(tc, "cool-sdk"))).
				WithNewFile("main.go", `package main

import "os"

type Test struct {}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
`,
				)

			out, err := ctr.
				With(daggerCall("fn")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
	})

	t.Run("module initialization", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// verify that SDKs can successfully:
		// - create an exec during module initialization
		// - call CurrentModule().Source
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/coolsdk").
			With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"encoding/json"

	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct {}


func (m *CoolSdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
	// return hardcoded typedefs; this module will thus only work during init, but that's all we're testing here
	mod := dag.Module().WithObject(dag.TypeDef().
		WithObject("Test").
		WithFunction(dag.Function("CoolFn", dag.TypeDef().WithKind(dagger.TypeDefKindVoidKind).WithOptional(true))))
	modID, err := mod.ID(ctx)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(modID)
	if err != nil {
		return nil, err
	}
	return dag.Container().
		From("alpine").
		WithNewFile(outputFilePath, string(b)).
		WithEntrypoint([]string{
			"sh", "-c", "",
		}), nil
}

func (m *CoolSdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
			WithNewFile("main.go", `package main

type Test struct {}
`,
			)

		out, err := ctr.
			With(daggerFunctions()).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `cool-fn`) // hardcoded typedef
	})
}

// TestUnbundleSDK verifies that you can implement a SDK without
// having to implements the full interface but only the ones you want.
// cc: https://github.com/dagger/dagger/issues/7707
func (ModuleSuite) TestUnbundleSDK(ctx context.Context, t *testctx.T) {
	t.Run("only codegen", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory("/work/sdk", c.Host().Directory("./testdata/sdks/only-codegen")).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=./sdk", "--source=."))

		t.Run("can run dagger develop", func(ctx context.Context, t *testctx.T) {
			generatedFile, err := ctr.With(daggerExec("develop")).File("/work/hello.txt").Contents(ctx)

			require.NoError(t, err)
			require.Equal(t, "Hello, world!", generatedFile)
		})

		t.Run("explicit error on dagger call", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerExec("call", "foo")).Sync(ctx)

			requireErrOut(t, err, `"./sdk" SDK does not support defining and executing functions`)
		})

		t.Run("explicit error on dagger functions", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerFunctions()).Sync(ctx)

			requireErrOut(t, err, `"./sdk" SDK does not support defining and executing functions`)
		})
	})

	t.Run("only runtime", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory("/work/sdk", c.Host().Directory("./testdata/sdks/only-runtime")).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=./sdk", "--source=."))

		t.Run("can run dagger develop without failing", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerExec("develop")).Sync(ctx)

			require.NoError(t, err)
		})

		t.Run("can run dagger functions", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerFunctions()).Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "hello-world")
		})

		t.Run("can run dagger call", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("hello-world")).Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "Hello world")
		})
	})
}

// TestHostError verifies the host api is not exposed to modules
func (ModuleSuite) TestHostError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", `package main
 			import (
 				"context"
				"dagger/test/internal/dagger"
 			)
 			type Test struct {}
 			func (m *Test) Fn(ctx context.Context) *dagger.Directory {
 				return dag.Host().Directory(".")
 			}
 			`,
		).
		With(daggerCall("fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Host undefined")
}

// TestEngineError verifies the engine api is not exposed to modules
func (ModuleSuite) TestEngineError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Fn(ctx context.Context) error {
 				_, _ = dag.Engine().LocalCache().EntrySet().Entries(ctx)
				return nil
 			}
 			`,
		).
		With(daggerCall("fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Engine undefined")
}

func (ModuleSuite) TestDaggerListen(ctx context.Context, t *testctx.T) {
	t.Run("with mod", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		addr := "127.0.0.1:12456"
		listenCmd := hostDaggerCommand(ctx, t, modDir, "listen", "--listen", addr)
		listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")
		listenCmd.Stdout = testutil.NewTWriter(t)
		listenCmd.Stderr = testutil.NewTWriter(t)
		require.NoError(t, listenCmd.Start())

		backoff.Retry(func() error {
			c, err := net.Dial("tcp", addr)
			t.Log("dial", addr, c, err)
			if err != nil {
				return err
			}
			return c.Close()
		}, backoff.NewExponentialBackOff(
			backoff.WithMaxElapsedTime(time.Minute),
		))

		callCmd := hostDaggerCommand(ctx, t, modDir, "call", "container-echo", "--string-arg=hi", "stdout")
		callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12456", "DAGGER_SESSION_TOKEN=lol")
		callCmd.Stderr = testutil.NewTWriter(t)
		out, err := callCmd.Output()
		require.NoError(t, err)
		lines := strings.Split(string(out), "\n")
		lastLine := lines[len(lines)-2]
		require.Equal(t, "hi", lastLine)
	})

	t.Run("disable read write", func(ctx context.Context, t *testctx.T) {
		t.Run("with mod", func(ctx context.Context, t *testctx.T) {
			// mod load fails but should still be able to query base api

			modDir := t.TempDir()
			_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
			require.NoError(t, err)

			listenCmd := hostDaggerCommand(ctx, t, modDir, "listen", "--disable-host-read-write", "--listen", "127.0.0.1:12457")
			listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")
			require.NoError(t, listenCmd.Start())

			var out []byte
			for range limitTicker(time.Second, 60) {
				callCmd := hostDaggerCommand(ctx, t, modDir, "query")
				callCmd.Stdin = strings.NewReader(fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
				callCmd.Stderr = testutil.NewTWriter(t)
				callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12457", "DAGGER_SESSION_TOKEN=lol")
				out, err = callCmd.Output()
				if err == nil {
					require.Contains(t, string(out), distconsts.AlpineVersion)
					return
				}
				time.Sleep(1 * time.Second)
			}
			t.Fatalf("failed to call query: %s err: %v", string(out), err)
		})

		t.Run("without mod", func(ctx context.Context, t *testctx.T) {
			tmpdir := t.TempDir()

			listenCmd := hostDaggerCommand(ctx, t, tmpdir, "listen", "--disable-host-read-write", "--listen", "127.0.0.1:12458")
			listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")
			require.NoError(t, listenCmd.Start())

			var out []byte
			var err error
			for range limitTicker(time.Second, 60) {
				callCmd := hostDaggerCommand(ctx, t, tmpdir, "query")
				callCmd.Stdin = strings.NewReader(fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
				callCmd.Stderr = testutil.NewTWriter(t)
				callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12458", "DAGGER_SESSION_TOKEN=lol")
				out, err = callCmd.Output()
				if err == nil {
					require.Contains(t, string(out), distconsts.AlpineVersion)
					return
				}
				time.Sleep(1 * time.Second)
			}
			t.Fatalf("failed to call query: %s err: %v", string(out), err)
		})
	})
}

func (ModuleSuite) TestSecretNested(ctx context.Context, t *testctx.T) {
	t.Run("pass secrets between modules", func(ctx context.Context, t *testctx.T) {
		// check that we can pass valid secret objects between functions in
		// different modules

		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/secreter/internal/dagger"
)

type Secreter struct {}

func (_ *Secreter) Make() *dagger.Secret {
	return dag.SetSecret("FOO", "inner")
}

func (_ *Secreter) Get(ctx context.Context, secret *dagger.Secret) (string, error) {
	return secret.Plaintext(ctx)
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) TryReturn(ctx context.Context) error {
	text, err := dag.Secreter().Make().Plaintext(ctx)
	if err != nil {
		return err
	}
	if text != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", text)
	}
	return nil
}

func (t *Toplevel) TryArg(ctx context.Context) error {
	text, err := dag.Secreter().Get(ctx, dag.SetSecret("BAR", "outer"))
	if err != nil {
		return err
	}
	if text != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", text)
	}
	return nil
}
`,
			)

		t.Run("can pass secrets", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{tryArg}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("can return secrets", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{tryReturn}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("dockerfiles in modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("/input/Dockerfile", `FROM `+alpineImage+`
RUN --mount=type=secret,id=my-secret test "$(cat /run/secrets/my-secret)" = "barbar"
`).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
}

func (t *Test) Ctr(src *dagger.Directory) *dagger.Container {
	secret := dag.SetSecret("my-secret", "barbar")
	return src.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Secrets: []*dagger.Secret{secret},
		}).
		WithExec([]string{"true"}) // needed to avoid "no command set" error
}

func (t *Test) Evaluated(ctx context.Context, src *dagger.Directory) error {
	secret := dag.SetSecret("my-secret", "barbar")
	_, err := src.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Secrets: []*dagger.Secret{secret},
		}).
		WithExec([]string{"true"}).
		Sync(ctx)
	return err
}
`)

		_, err := ctr.
			With(daggerCall("ctr", "--src", "/input", "stdout")).
			Sync(ctx)
		require.NoError(t, err)

		_, err = ctr.
			With(daggerCall("evaluated", "--src", "/input")).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("pass embedded secrets between modules", func(ctx context.Context, t *testctx.T) {
		// check that we can pass valid secret objects between functions in
		// different modules when the secrets are embedded in containers rather than
		// passed directly

		t.Run("embedded in returns", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (*Dep) GetEncoded(ctx context.Context) *dagger.Container {
	secret := dag.SetSecret("FOO", "shhh")
	return dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"})
}

func (*Dep) GetCensored(ctx context.Context) *dagger.Container {
	secret := dag.SetSecret("BAR", "fdjsklajakldjfl")
	return dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET"})
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	return dag.Dep().GetEncoded().Stdout(ctx)
}

func (t *Test) GetCensored(ctx context.Context) (string, error) {
	return dag.Dep().GetCensored().Stdout(ctx)
}
`,
				)

			encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
			require.NoError(t, err)
			require.Equal(t, "shhh\n", string(decoded))

			censoredOut, err := ctr.With(daggerCall("get-censored")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "***\n", censoredOut)
		})

		t.Run("embedded in args", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (*Dep) Get(ctx context.Context, ctr *dagger.Container) (string, error) {
	return ctr.Stdout(ctx)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	secret := dag.SetSecret("FOO", "shhh")
	ctr := dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"})
	return dag.Dep().Get(ctx, ctr)
}

func (t *Test) GetCensored(ctx context.Context) (string, error) {
	secret := dag.SetSecret("BAR", "fdlaskfjdlsajfdkasl")
	ctr := dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET"})
	return dag.Dep().Get(ctx, ctr)
}
`,
				)

			encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
			require.NoError(t, err)
			require.Equal(t, "shhh\n", string(decoded))

			censoredOut, err := ctr.With(daggerCall("get-censored")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "***\n", censoredOut)
		})

		t.Run("embedded through struct field", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	Secret *dagger.Secret
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from foo"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (m *Test) Test(ctx context.Context) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", "cat /mnt/secret | tr [a-z] [A-Z]"}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerCall("test")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "HELLO FROM FOO", out)
		})

		t.Run("embedded through private struct field", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	// +private
	Secret *dagger.Secret
	// +private
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from foo"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (m *Test) Test(ctx context.Context) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", "cat /mnt/secret | tr [a-z] [A-Z]"}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerCall("test")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "HELLO FROM FOO", out)
		})

		t.Run("double nested and called repeatedly", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			// Set up the base generator module
			ctr = ctr.
				WithWorkdir("/work/keychain/generator").
				With(daggerExec("init", "--name=generator-module", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
    "context"
    "dagger/generator-module/internal/dagger"
)

type GeneratorModule struct {
    // +private
    Password *dagger.Secret
}

func New() *GeneratorModule {
    return &GeneratorModule{
        Password: dag.SetSecret("pass", "admin"),
    }
}

func (m *GeneratorModule) Gen(ctx context.Context, name string) error {
    _, err := m.Password.Plaintext(ctx)
    return err
}
`)

			// Set up the keychain module that depends on generator
			ctr = ctr.
				WithWorkdir("/work/keychain").
				With(daggerExec("init", "--name=keychain", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./generator")).
				WithNewFile("main.go", `package main

import (
    "context"
)

type Keychain struct{}

func (m *Keychain) Get(ctx context.Context, name string) error {
    return dag.GeneratorModule().Gen(ctx, name)
}
`)

			// Set up the main module that uses keychain
			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=mymodule", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./keychain")).
				WithNewFile("main.go", `package main

import (
    "context"
    "fmt"
)

type Mymodule struct{}

func (m *Mymodule) Issue(ctx context.Context) error {
    kc := dag.Keychain()

    err := kc.Get(ctx, "a")
    if err != nil {
        return fmt.Errorf("first get: %w", err)
    }

    err = kc.Get(ctx, "a")
    if err != nil {
        return fmt.Errorf("second get, same args: %w", err)
    }

    err = kc.Get(ctx, "b")
    if err != nil {
        return fmt.Errorf("third get: %w", err)
    }
    return nil
}
`)

			// Test that repeated calls work correctly
			_, err := ctr.With(daggerCall("issue")).Sync(ctx)
			require.NoError(t, err)
		})

		t.Run("cached", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	Secret *dagger.Secret
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from mount"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
  "fmt"
)

type Test struct {}

func (m *Test) Foo(ctx context.Context) (string, error) {
  return m.impl(ctx, "foo")
}

func (m *Test) Bar(ctx context.Context) (string, error) {
  return m.impl(ctx, "bar")
}

func (m *Test) impl(ctx context.Context, name string) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", fmt.Sprintf("(echo %s && cat /mnt/secret) | tr [a-z] [A-Z]", name)}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerQuery("{foo,bar}")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo": "FOO\nHELLO FROM MOUNT", "bar": "BAR\nHELLO FROM MOUNT"}`, out)
		})
	})

	t.Run("parent fields", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	Ctr *dagger.Container
}

func (t *Test) FnA() *Test {
	secret := dag.SetSecret("FOO", "omg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) FnB(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("fn-a", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omg\n", string(decoded))
	})

	t.Run("private parent fields", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	// +private
	Ctr *dagger.Container
}

func (t *Test) FnA() *Test {
	secret := dag.SetSecret("FOO", "omg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) FnB(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("fn-a", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omg\n", string(decoded))
	})

	t.Run("parent field set in constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	Ctr *dagger.Container
}

func New() *Test {
	t := &Test{}
	secret := dag.SetSecret("FOO", "omfg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omfg\n", string(decoded))
	})

	t.Run("duplicate secret names", func(ctx context.Context, t *testctx.T) {
		// check that each module has it's own segmented secret store, by
		// writing secrets with the same name

		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/maker").
			With(daggerExec("init", "--name=maker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/maker/internal/dagger"
)

type Maker struct {}

func (_ *Maker) MakeSecret(ctx context.Context) (*dagger.Secret, error) {
	secret := dag.SetSecret("FOO", "inner")
	_, err := secret.ID(ctx)  // force the secret into the store
	if err != nil {
		return nil, err
	}
	return secret, nil
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./maker")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context) error {
	secret := dag.SetSecret("FOO", "outer")
	_, err := secret.ID(ctx)  // force the secret into the store
	if err != nil {
		return err
	}

	// this creates an inner secret "FOO", but it mustn't overwrite the outer one
	secret2 := dag.Maker().MakeSecret()

	plaintext, err := secret.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", plaintext)
	}

	plaintext, err = secret2.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", plaintext)
	}

	return nil
}
`,
			)

		_, err := ctr.With(daggerQuery(`{attempt}`)).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("secret by id leak", func(ctx context.Context, t *testctx.T) {
		// check that modules can't access each other's global secret stores,
		// even when we know the underlying IDs

		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/leaker").
			With(daggerExec("init", "--name=leaker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"

	"dagger/leaker/internal/dagger"
)

type Leaker struct {}

func (l *Leaker) Leak(ctx context.Context, target string) string {
	secret, _ := dag.LoadSecretFromID(dagger.SecretID(target)).Plaintext(ctx)
	return secret
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./leaker")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context, uniq string) error {
	secretID, err := dag.SetSecret("mysecret", "asdfasdf").ID(ctx)
	if err != nil {
		return err
	}

	// loading secret-by-id in the same module should succeed
	plaintext, err := dag.LoadSecretFromID(secretID).Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "asdfasdf" {
		return fmt.Errorf("expected \"asdfasdf\", but got %q", plaintext)
	}

	// but getting a leaker module to do this should fail
	plaintext, err = dag.Leaker().Leak(ctx, string(secretID))
	if err != nil {
		return err
	}
	if plaintext != "" {
		return fmt.Errorf("expected \"\", but got %q", plaintext)
	}

	return nil
}
`,
			)

		_, err := ctr.With(daggerQuery(`{attempt(uniq: %q)}`, identity.NewID())).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("secrets cache normally", func(ctx context.Context, t *testctx.T) {
		// check that secrets cache as they would without nested modules,
		// which is essentially dependent on whether they have stable IDs

		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import "dagger/secreter/internal/dagger"

type Secreter struct {}

func (_ *Secreter) Make(uniq string) *dagger.Secret {
	return dag.SetSecret("MY_SECRET", uniq)
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", fmt.Sprintf(`package main

import (
	"context"
	"fmt"
	"dagger/toplevel/internal/dagger"
)

type Toplevel struct {}

func (_ *Toplevel) AttemptInternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.SetSecret("MY_SECRET", "foo"),
		dag.SetSecret("MY_SECRET", "bar"),
	)
}

func (_ *Toplevel) AttemptExternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.Secreter().Make("foo"),
		dag.Secreter().Make("bar"),
	)
}

func diffSecret(ctx context.Context, first, second *dagger.Secret) error {
	firstOut, err := dag.Container().
		From("%[1]s").
		WithSecretVariable("VAR", first).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	secondOut, err := dag.Container().
		From("%[1]s").
		WithSecretVariable("VAR", second).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	if firstOut != secondOut {
		return fmt.Errorf("%%q != %%q", firstOut, secondOut)
	}
	return nil
}
`, alpineImage),
			)

		t.Run("internal secrets cache", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{attemptInternal}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("external secrets cache", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{attemptExternal}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("optional secret field on module object", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pythonSource(`
import base64
import dagger
from dagger import dag, field, function, object_type


@object_type
class Test:
    @function
    def getobj(self, *, top_secret: dagger.Secret | None = None) -> "Obj":
        return Obj(top_secret=top_secret)


@object_type
class Obj:
    top_secret: dagger.Secret | None = field(default=None)

    @function
    async def getSecret(self) -> str:
        plaintext = await self.top_secret.plaintext()
        return base64.b64encode(plaintext.encode()).decode()
`)).
			With(daggerInitPython()).
			WithEnvVariable("TOP_SECRET", "omg").
			With(daggerCall("getobj", "--top-secret", "env://TOP_SECRET", "get-secret")).
			Stdout(ctx)

		require.NoError(t, err)
		decodeOut, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
		require.NoError(t, err)
		require.Equal(t, "omg", string(decodeOut))
	})
}

func (ModuleSuite) TestUnicodePath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/wórk/sub/").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/wórk/sub/main.go", `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Hello(ctx context.Context) string {
				return "hello"
 			}
 			`,
		).
		With(daggerQuery(`{hello}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"hello":"hello"}`, out)
}

func (ModuleSuite) TestStartServices(ctx context.Context, t *testctx.T) {
	// regression test for https://github.com/dagger/dagger/pull/6914
	t.Run("use service in multiple functions", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", fmt.Sprintf(`package main

	import (
		"context"
		"fmt"
		"dagger/test/internal/dagger"
	)

	type Test struct {
	}

	func (m *Test) FnA(ctx context.Context) (*Sub, error) {
		svc := dag.Container().
			From("python").
			WithMountedDirectory(
				"/srv/www",
				dag.Directory().WithNewFile("index.html", "hey there"),
			).
			WithWorkdir("/srv/www").
			WithExposedPort(23457).
			WithDefaultArgs([]string{"python", "-m", "http.server", "23457"}).
			AsService()

		ctr := dag.Container().
			From("%s").
			WithServiceBinding("svc", svc).
			WithExec([]string{"wget", "-O", "-", "http://svc:23457"})

		out, err := ctr.Stdout(ctx)
		if err != nil {
			return nil, err
		}
		if out != "hey there" {
			return nil, fmt.Errorf("unexpected output: %%q", out)
		}
		return &Sub{Ctr: ctr}, nil
	}

	type Sub struct {
		Ctr *dagger.Container
	}

	func (m *Sub) FnB(ctx context.Context) (string, error) {
		return m.Ctr.
			WithExec([]string{"wget", "-O", "-", "http://svc:23457"}).
			Stdout(ctx)
	}
	`, alpineImage),
			).
			With(daggerCall("fn-a", "fn-b")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey there", strings.TrimSpace(out))
	})

	// regression test for https://github.com/dagger/dagger/issues/6951
	t.Run("service in multiple containers", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", fmt.Sprintf(`package main
import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
}

func (m *Test) Fn(ctx context.Context) *dagger.Container {
	redis := dag.Container().
		From("redis").
		WithExposedPort(6379).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	cli := dag.Container().
		From("redis").
		WithoutEntrypoint().
		WithServiceBinding("redis", redis)

	ctrA := cli.WithExec([]string{"sh", "-c", "redis-cli -h redis info >> /tmp/out.txt"})

	file := ctrA.Directory("/tmp").File("/out.txt")

	ctrB := dag.Container().
		From("%s").
		WithFile("/out.txt", file)

	return ctrB.WithExec([]string{"cat", "/out.txt"})
}
	`, alpineImage),
			).
			With(daggerCall("fn", "stdout")).
			Sync(ctx)
		require.NoError(t, err)
	})
}

func (ModuleSuite) TestReturnNilField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go")).
		With(sdkSource("go", `package main

type Test struct {
	A *Thing
	B *Thing
}

type Thing struct{}

func New() *Test {
	return &Test{
		A: &Thing{},
	}
}

func (m *Test) Hello() string {
	return "Hello"
}

`)).
		With(daggerCall("hello")).
		Sync(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestGetEmptyField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("without constructor", func(ctx context.Context, t *testctx.T) {
		out, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go")).
			With(sdkSource("go", `package main

import "dagger/test/internal/dagger"

type Test struct {
	A string
	B int
	C *dagger.Container
	D dagger.ImageLayerCompression
	E dagger.Platform
}

`)).
			With(daggerQuery("{a,b}")).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"a": "", "b": 0}`, out)
		// NOTE:
		// - trying to get C will try and decode an empty ID
		// - trying to get D will fail to instantiate an empty enum
		// - trying to get E will fail to parse the platform
		// ...but, we should be able to get the other values (important for backwards-compat)
	})

	t.Run("with constructor", func(ctx context.Context, t *testctx.T) {
		out, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go")).
			With(sdkSource("go", `package main

import "dagger/test/internal/dagger"

type Test struct {
	A string
	B int
	C *dagger.Container
	// these aren't tested here, since we can't give them zero values in the constructor
	// D dagger.ImageLayerCompression
	// E dagger.Platform
}

func New() *Test {
	return &Test{}
}
`)).
			With(daggerQuery("{a,b}")).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"a": "", "b": 0}`, out)
		// NOTE:
		// - trying to get C will try and decode an empty ID
		// ...but, we should be able to get the other values (important for backwards-compat)
	})
}

func (ModuleSuite) TestModuleSchemaVersion(ctx context.Context, t *testctx.T) {
	t.Run("standalone", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)

		require.NotEmpty(t, gjson.Get(out, "__schemaVersion").String())
	})

	t.Run("standalone explicit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0").
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"__schemaVersion":"v2.0.0"}`, out)
	})

	t.Run("standalone explicit dev", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0-dev-123").
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"__schemaVersion":"v2.0.0"}`, out)
	})

	t.Run("module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.11.0"}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			)
		out, err := work.
			With(daggerQuery("{getVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getVersion": "v0.11.0"}`, out)

		out, err = work.
			With(daggerCall("get-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "v0.11.0")
	})

	t.Run("module deps", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
			WithNewFile("dagger.json", `{"name": "dep", "sdk": "go", "source": ".", "engineVersion": "v0.11.0"}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Dep struct {}

func (m *Dep) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./dep")).
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.10.0", "dependencies": [{"name": "dep", "source": "dep"}]}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	myVersion, err := schemaVersion(ctx)
	if err != nil {
		return "", err
	}
	depVersion, err := dag.Dep().GetVersion(ctx)
	if err != nil {
		return "", err
	}
	return myVersion + " " + depVersion, nil
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			)

		out, err := work.
			With(daggerQuery("{getVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getVersion": "v0.10.0 v0.11.0"}`, out)

		out, err = work.
			With(daggerCall("get-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "v0.10.0 v0.11.0")
	})
}

func (ModuleSuite) TestModulePreFilteringDirectory(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	t.Run("pre filtering directory on module call", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (t *Test) Call(
  // +ignore=[
  //   "foo.txt",
  //   "bar"
  // ]
  dir *dagger.Directory,
) *dagger.Directory {
 return dir
}`,
			},
			{
				sdk: "typescript",
				source: `import { object, func, Directory, argument } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  call(
    @argument({ ignore: ["foo.txt", "bar"] }) dir: Directory,
  ): Directory {
    return dir
  }
}`,
			},
			{
				sdk: "python",
				source: `from typing import Annotated

import dagger
from dagger import DefaultPath, Ignore, function, object_type


@object_type
class Test:
    @function
    async def call(
        self,
        dir: Annotated[dagger.Directory, Ignore(["foo.txt","bar"])],
    ) -> dagger.Directory:
        return dir
`,
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := goGitBase(t, c).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					// Add inputs
					WithDirectory("/work/input", c.
						Directory().
						WithNewFile("foo.txt", "foo").
						WithNewFile("bar.txt", "bar").
						WithDirectory("bar", c.Directory().WithNewFile("baz.txt", "baz"))).
					// Add dep
					WithWorkdir("/work/dep").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
					With(sdkSource(tc.sdk, tc.source)).
					// Setup test modules
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test-mod", "--sdk=go", "--source=.")).
					With(daggerExec("install", "./dep")).
					With(sdkSource("go", `package main

import (
	"dagger/test-mod/internal/dagger"
)

type TestMod struct {}

func (t *TestMod) Test(
  dir *dagger.Directory,
) *dagger.Directory {
 return dag.Test().Call(dir)
}`,
					))

				out, err := modGen.With(daggerCall("test", "--dir", "./input", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\n", out)
			})
		}
	})
}

func (ModuleSuite) TestFloat(ctx context.Context, t *testctx.T) {
	depSrc := `package main

type Dep struct{}

func (m *Dep) Dep(n float64) float32 {
	return float32(n)
}
`

	type testCase struct {
		sdk    string
		source string
	}

	testCases := []testCase{
		{
			sdk: "go",
			source: `package main

import "context"

type Test struct{}

func (m *Test) Test(n float64) float64 {
	return n
}

func (m *Test) TestFloat32(n float32) float32 {
	return n
}

func (m *Test) Dep(ctx context.Context, n float64) (float64, error) {
	return dag.Dep().Dep(ctx, n)
}`,
		},
		{
			sdk: "typescript",
			source: `import { dag, float, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  test(n: float): float {
    return n
  }

  @func()
  testFloat32(n: float): float {
    return n
  }

  @func()
  async dep(n: float): Promise<float> {
    return dag.dep().dep(n)
  }
}`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import dag

@dagger.object_type
class Test:
    @dagger.function
    def test(self, n: float) -> float:
        return n

    @dagger.function
    def testFloat32(self, n: float) -> float:
        return n

    @dagger.function
    async def dep(self, n: float) -> float:
        return await dag.dep().dep(n)
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("/work/dep/main.go", depSrc).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			t.Run("float64", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test", "--n=3.14")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `3.14`, out)
			})

			t.Run("float32", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-float-32", "--n=1.73424")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `1.73424`, out)
			})

			t.Run("call dep with float64 to float32 conversion", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("dep", "--n=232.3454")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `232.3454`, out)
			})
		})
	}
}

func (ModuleSuite) TestModuleDevelopVersion(ctx context.Context, t *testctx.T) {
	moduleSrc := `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}`

	t.Run("from low", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("from high", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v100.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		_, err := work.
			File("dagger.json").
			Contents(ctx)

		// sadly, just no way to handle this :(
		// in the future, the format of dagger.json might change dramatically,
		// and so there's no real way to know from the older version how to
		// convert it back down
		require.Error(t, err)
		requireErrOut(t, err, `module requires dagger v100.0.0, but you have`)
	})

	t.Run("from missing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("to specified", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.0.0"}`)

		work = work.With(daggerExec("develop", "--compat=v0.9.9"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "v0.9.9", gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("skipped", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.9.9"}`)

		work = work.With(daggerExec("develop", "--compat"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "v0.9.9", gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in install", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("install", "github.com/shykes/hello"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in uninstall", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "dependencies": [{ "name": "hello", "source": "github.com/shykes/hello", "pin": "2d789671a44c4d559be506a9bc4b71b0ba6e23c9" }], "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("uninstall", "hello"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in update", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("update"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})
}

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

			// Secondary object type + field deprecation: LegacyRecord.note
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

			// Interface presence + deprecation on its method
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

func (ModuleSuite) TestLoadWhenNoModule(ctx context.Context, t *testctx.T) {
	// verify that if a module is loaded from a directory w/ no module we don't
	// load extra files
	c := connect(ctx, t)

	tmpDir := t.TempDir()
	fileName := "foo"
	filePath := filepath.Join(tmpDir, fileName)
	require.NoError(t, os.WriteFile(filePath, []byte("foo"), 0o644))

	ents, err := c.ModuleSource(tmpDir).ContextDirectory().Entries(ctx)
	require.NoError(t, err)
	require.Empty(t, ents)
}

func (ModuleSuite) TestSSHAgentConnection(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("ConcurrentSetupAndCleanup", func(ctx context.Context, t *testctx.T) {
			var wg sync.WaitGroup
			for range 100 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, cleanup := setupPrivateRepoSSHAgent(t)
					time.Sleep(10 * time.Millisecond) // Simulate some work
					cleanup()
				}()
			}
			wg.Wait()
		})
	})
}

func (ModuleSuite) TestSSHAuthSockPathHandling(ctx context.Context, t *testctx.T) {
	tc := getVCSTestCase(t, "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")

	t.Run("SSH auth with home expansion and symlink", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		privateSetup, cleanup := privateRepoSetup(c, t, tc)
		defer cleanup()

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(privateSetup).
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithEnvVariable("HOME", "/home/dagger").
			WithExec([]string{"ln", "-s", "/sock/unix-socket", "/home/dagger/.ssh-sock"}).
			WithEnvVariable("SSH_AUTH_SOCK", "~/.ssh-sock")

		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithExec([]string{"sh", "-c", "cd", "/work/some/subdir"}).
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})

	t.Run("SSH auth from different relative paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		privateSetup, cleanup := privateRepoSetup(c, t, tc)
		defer cleanup()

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(privateSetup).
			WithExec([]string{"mkdir", "-p", "/work/subdir"})

		// Test from same directory as the socket
		out, err := ctr.
			WithWorkdir("/sock").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		// Test from a subdirectory
		out, err = ctr.
			WithWorkdir("/work/subdir").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		// Test from parent directory
		out, err = ctr.
			WithWorkdir("/").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})
}

func (ModuleSuite) TestPrivateDeps(ctx context.Context, t *testctx.T) {
	t.Run("golang", func(ctx context.Context, t *testctx.T) {
		privateDepCode := `package main

import (
	"github.com/dagger/dagger-test-modules/privatedeps/pkg/cooldep"
)

type Foo struct{}

// Returns a container that echoes whatever string argument is provided
func (m *Foo) HowCoolIsDagger() string {
	return cooldep.HowCoolIsThat
}
`

		daggerjson := `{
  "name": "foo",
  "engineVersion": "v0.16.2",
  "sdk": {
    "source": "go",
    "config": {
      "goprivate": "github.com/dagger/dagger-test-modules"
    }
  }
}`

		c := connect(ctx, t)
		sockPath, cleanup := setupPrivateRepoSSHAgent(t)
		defer cleanup()

		socket := c.Host().UnixSocket(sockPath)

		// This is simulating a user's setup where they have
		// 1. ssh auth sock setup
		// 2. gitconfig file with insteadOf directive
		// 3. a dagger module that requires a dependency (NOT a dagger dependency) from a remote private repo.
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithExec([]string{"apk", "add", "git", "openssh", "openssl"}).
			WithUnixSocket("/sock/unix-socket", socket).
			WithEnvVariable("SSH_AUTH_SOCK", "/sock/unix-socket").
			WithWorkdir("/work").
			WithNewFile("/root/.gitconfig", `
[url "ssh://git@github.com/"]
	insteadOf = https://github.com/
`).
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			WithNewFile("main.go", privateDepCode).
			WithNewFile("dagger.json", daggerjson)

		howCoolIsDagger, err := modGen.
			With(daggerExec("call", "how-cool-is-dagger")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ubercool", howCoolIsDagger)
	})
}

func (ModuleSuite) TestLargeErrors(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()

	_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
	require.NoError(t, err)

	moduleSrc := `package main

import (
  "context"
)

type Test struct{}

func (m *Test) RunNoisy(ctx context.Context) error {
	_, err := dag.Container().
		From("` + alpineImage + `").
		WithExec([]string{"sh", "-c", ` + "`" + `
			for i in $(seq 100); do
				for j in $(seq 1024); do
					echo -n x
					echo -n y >/dev/stderr
				done
				echo
			done
			exit 42
		` + "`" + `}).
		Sync(ctx)
	return err
}
`
	err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(moduleSrc), 0o644)
	require.NoError(t, err)

	c := connect(ctx, t)

	err = c.ModuleSource(modDir).AsModule().Serve(ctx)
	require.NoError(t, err)

	_, err = testutil.QueryWithClient[struct {
		Test struct {
			RunNoisy any
		}
	}](c, t, `{test{runNoisy}}`, nil)
	var execError *dagger.ExecError
	require.ErrorAs(t, err, &execError)

	// if we get `2` here, that means we're getting the less helpful error:
	// process "/runtime" did not complete successfully: exit code: 2
	require.Equal(t, 42, execError.ExitCode)
	require.Contains(t, execError.Stdout, "xxxxx")
	require.Contains(t, execError.Stderr, "yyyyy")
}

func (ModuleSuite) TestReturnNil(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {
	Dirs []*dagger.Directory
}

func (m *Test) Nothing() (*dagger.Directory, error) {
	return nil, nil
}

func (m *Test) ListWithNothing() ([]*dagger.Directory, error) {
	return []*dagger.Directory{nil}, nil
}

func (m *Test) ObjsWithNothing() ([]*Test, error) {
	return []*Test{
		nil,
		{
			Dirs: []*dagger.Directory{nil},
		},
	}, nil
}
`,
		)

	out, err := modGen.With(daggerQuery(`{nothing{entries}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"nothing":null}`, out)

	out, err = modGen.With(daggerQuery(`{listWithNothing{entries}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"listWithNothing":[null]}`, out)

	out, err = modGen.With(daggerQuery(`{objsWithNothing{dirs{entries}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"objsWithNothing":[null,{"dirs":[null]}]}`, out)
}

func (ModuleSuite) TestFunctionCacheControl(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk    string
		source string
	}{
		{
			// TODO: add test that function doc strings still get parsed correctly, don't include //+ etc.
			sdk: "go",
			source: `package main

import (
	"crypto/rand"
)

type Test struct{}

// My cool doc on TestTtl
// +cache="40s"
func (m *Test) TestTtl() string {
	return rand.Text()
}

// My dope doc on TestCachePerSession
// +cache="session"
func (m *Test) TestCachePerSession() string {
	return rand.Text()
}

// My darling doc on TestNeverCache
// +cache="never"
func (m *Test) TestNeverCache() string {
	return rand.Text()
}

// My rad doc on TestAlwaysCache
func (m *Test) TestAlwaysCache() string {
	return rand.Text()
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
import random
import string

@dagger.object_type
class Test:
		@dagger.function(cache="40s")
		def test_ttl(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function(cache="session")
		def test_cache_per_session(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function(cache="never")
		def test_never_cache(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function
		def test_always_cache(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))
`,
		},

		{
			sdk: "typescript",
			source: `
import crypto from "crypto"

import {  object, func } from "@dagger.io/dagger"

@object()
export class Test {
	@func({ cache: "40s"})
	testTtl(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func({ cache: "session" })
	testCachePerSession(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func({ cache: "never" })
	testNeverCache(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func()
	testAlwaysCache(): string {
		return crypto.randomBytes(16).toString("hex")
	}
}

`,
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			t.Run("always cache", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				// TODO: this is gonna be flaky to cache prunes, might need an isolated engine

				out1, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()). // don't cache the nested execs themselves
					With(daggerCall("test-always-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-always-cache")).Stdout(ctx)
				require.NoError(t, err)

				require.Equal(t, out1, out2, "outputs should be equal since the result is always cached")
			})

			t.Run("cache per session", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1a, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				out1b, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, out1a, out1b, "outputs should be equal since they are from the same session")
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2a, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				out2b, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, out2a, out2b, "outputs should be equal since they are from the same session")

				require.NotEqual(t, out1a, out2a, "outputs should not be equal since they are from different sessions")
			})

			t.Run("never cache", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1a, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				out1b, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out1a, out1b, "outputs should not be equal since they are never cached")
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2a, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				out2b, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out2a, out2b, "outputs should not be equal since they are never cached")

				require.NotEqual(t, out1a, out2a, "outputs should not be equal since they are never cached")
			})

			// TODO: this is gonna be hella flaky probably, need isolated engine to combat pruning and probably more generous times...
			t.Run("cache ttl", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c2.Close())

				require.Equal(t, out1, out2, "outputs should be equal since the cache ttl has not expired")
				time.Sleep(41 * time.Second)

				c3 := connect(ctx, t)
				modGen3 := modInit(t, c3, tc.sdk, tc.source)

				out3, err := modGen3.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out1, out3, "outputs should not be equal since the cache ttl has expired")
			})
		})
	}

	// rest of tests are SDK agnostic so just test w/ go
	t.Run("setSecret invalidates cache", func(ctx context.Context, t *testctx.T) {
		const modSDK = "go"
		const modSrc = `package main

import (
	"crypto/rand"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) TestSetSecret() *dagger.Container {
	r := rand.Text()
	s := dag.SetSecret(r, r)
	return dag.Container().
		From("` + alpineImage + `").
		WithSecretVariable("TOP_SECRET", s)
}
`

		// in memory cache should be hit within a session, but
		// no cache hits across sessions should happen

		c1 := connect(ctx, t)
		modGen1 := modInit(t, c1, modSDK, modSrc)

		out1a, err := modGen1.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		out1b, err := modGen1.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out1a, out1b)
		require.NoError(t, c1.Close())

		c2 := connect(ctx, t)
		modGen2 := modInit(t, c2, modSDK, modSrc)

		out2a, err := modGen2.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		out2b, err := modGen2.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out2a, out2b)

		require.NotEqual(t, out1a, out2a)
	})

	t.Run("dependency contextual arg", func(ctx context.Context, t *testctx.T) {
		const modSDK = "go"
		const modSrc = `package main
import (
	"context"
	"dagger/test/internal/dagger"
)
type Test struct{}
func (m *Test) CallDep(ctx context.Context, cacheBust string) (*dagger.Directory, error) {
	return dag.Dep().Test().Sync(ctx)
}
func (m *Test) CallDepFile(ctx context.Context, cacheBust string) (*dagger.Directory, error) {
	return dag.Dep().TestFile().Sync(ctx)
}
`

		const depSrc = `package main
import (
	"dagger/dep/internal/dagger"
)
type Dep struct{}
func (m *Dep) Test() *dagger.Directory {
	return dag.Depdep().Test()
}
func (m *Dep) TestFile() *dagger.Directory {
	return dag.Depdep().TestFile()
}
`

		const depDepSrc = `package main
import (
	"crypto/rand"
	"dagger/depdep/internal/dagger"
)
type Depdep struct{}
func (m *Depdep) Test(
	// +defaultPath="."
	dir *dagger.Directory,
) *dagger.Directory {
	return dir.WithNewFile("rand.txt", rand.Text())
}
func (m *Depdep) TestFile(
	// +defaultPath="dagger.json"
	f *dagger.File,
) *dagger.Directory {
	return dag.Directory().
		WithFile("dagger.json", f).
		WithNewFile("rand.txt", rand.Text())
}
`

		getModGen := func(c *dagger.Client) *dagger.Container {
			return goGitBase(t, c).
				WithWorkdir("/work/depdep").
				With(daggerExec("init", "--name=depdep", "--sdk="+modSDK, "--source=.")).
				WithNewFile("/work/depdep/main.go", depDepSrc).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk="+modSDK, "--source=.")).
				With(daggerExec("install", "../depdep")).
				WithNewFile("/work/dep/main.go", depSrc).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+modSDK, "--source=.")).
				With(sdkSource(modSDK, modSrc)).
				With(daggerExec("install", "./dep"))
		}

		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			out1, err := getModGen(c1).
				With(daggerCall("call-dep", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.NoError(t, c1.Close())

			c2 := connect(ctx, t)
			out2, err := getModGen(c2).
				With(daggerCall("call-dep", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)

			require.Equal(t, out1, out2)
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			out1, err := getModGen(c1).
				With(daggerCall("call-dep-file", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.NoError(t, c1.Close())

			c2 := connect(ctx, t)
			out2, err := getModGen(c2).
				With(daggerCall("call-dep-file", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)

			require.Equal(t, out1, out2)
		})
	})

	t.Run("git contextual arg", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()

		// Initialize git repo
		gitCmd := exec.Command("git", "init")
		gitCmd.Dir = modDir
		gitOutput, err := gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "config", "user.email", "dagger@example.com")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "config", "user.name", "Dagger Tests")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		// Initialize dagger module
		initCmd := hostDaggerCommand(ctx, t, modDir, "init", "--name=test", "--sdk=go", "--source=.")
		initOutput, err := initCmd.CombinedOutput()
		require.NoError(t, err, string(initOutput))

		installCmd := hostDaggerCommand(ctx, t, modDir, "install",
			"github.com/dagger/dagger-test-modules/contextual-git-bug@"+vcsTestCaseCommit)
		installOutput, err := installCmd.CombinedOutput()
		require.NoError(t, err, string(installOutput))

		// Write module source
		err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(`package main

import (
    "context"
    "dagger/test/internal/dagger"
)

type Test struct {
    //+private
    Ref *dagger.GitRef
    //+private
    Dep *dagger.Dep
}

func New(
    // +defaultPath="."
    ref *dagger.GitRef,
    //+defaultPath="crap"
    source *dagger.Directory,
) *Test {
    return &Test{
        Ref: ref,
        Dep: dag.Dep(source),
    }
}

func (m *Test) Fn(
    ctx context.Context,
    //+defaultPath="config/config.local.js"
    configFile *dagger.File,
) (*dagger.Directory, error) {
    return m.Dep.WithRef(m.Ref).Fn().WithFile("config.js", configFile).Sync(ctx)
}
`), 0644)
		require.NoError(t, err)

		// Create git commit
		gitCmd = exec.Command("git", "add", ".")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		gitCmd = exec.Command("git", "commit", "-m", "make HEAD exist")
		gitCmd.Dir = modDir
		gitOutput, err = gitCmd.CombinedOutput()
		require.NoError(t, err, string(gitOutput))

		// Create directories and config file
		require.NoError(t, os.MkdirAll(filepath.Join(modDir, "crap"), 0755))
		require.NoError(t, os.MkdirAll(filepath.Join(modDir, "config"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(modDir, "config", "config.local.js"), []byte("1"), 0644))

		// Run first dagger call
		callCmd := hostDaggerCommand(ctx, t, modDir, "call", "fn")
		callOutput, err := callCmd.CombinedOutput()
		require.NoError(t, err, string(callOutput))

		// Update config file
		require.NoError(t, os.WriteFile(filepath.Join(modDir, "config", "config.local.js"), []byte("2"), 0644))

		// Run second dagger call
		callCmd = hostDaggerCommand(ctx, t, modDir, "call", "fn")
		callOutput, err = callCmd.CombinedOutput()
		require.NoError(t, err, string(callOutput))
	})
}

func (ModuleSuite) TestNestedClientCreatedByModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, cli *dagger.File, modDir *dagger.Directory) (string, error) {
	return dag.Container().From("`+alpineImage+`").
		WithMountedFile("/bin/dagger", cli).
		WithMountedDirectory("/dir", modDir).
		WithWorkdir("/dir").
		WithExec([]string{"dagger", "develop", "--recursive"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		WithExec([]string{"dagger", "call", "str"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
}

func (m *Test) Str() string {
	return "yoyoyo"
}
`,
		).
		WithWorkdir("/work/some/sub/dir").
		With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
		WithWorkdir("/work").
		With(daggerExec("install", "./some/sub/dir"))

	out, err := modGen.
		With(daggerCall("fn",
			"--cli", testCLIBinPath,
			"--modDir", ".",
		)).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "yoyoyo", out)
}
