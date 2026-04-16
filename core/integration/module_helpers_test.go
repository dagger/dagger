package core

// Workspace alignment: aligned; helper-only file for exact module-era command and fixture helpers.
// Scope: Explicit module command helpers, raw CLI helpers, module fixtures, and module introspection helpers.
// Intent: Keep post-workspace module tests on exact-by-intent helpers while legacy rewrite shims live separately.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core/modules"
	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

func daggerExecRaw(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerModuleExec(args ...string) dagger.WithContainerFunc {
	return daggerExecRaw(append([]string{"module"}, args...)...)
}

func daggerClientInstall(generator string) dagger.WithContainerFunc {
	return daggerExecRaw("client", "install", generator)
}

func daggerClientInstallAt(generator string, outputDirPath string) dagger.WithContainerFunc {
	return daggerExecRaw("client", "install", generator, outputDirPath)
}

func daggerQuery(query string, args ...any) dagger.WithContainerFunc {
	return daggerQueryAt("", query, args...)
}

func daggerQueryAt(modPath string, query string, args ...any) dagger.WithContainerFunc {
	query = fmt.Sprintf(query, args...)
	return func(c *dagger.Container) *dagger.Container {
		execArgs := []string{"dagger", "query"}
		if modPath != "" {
			execArgs = append(execArgs, "-m", modPath)
		}
		return c.WithExec(execArgs, dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerCall(args ...string) dagger.WithContainerFunc {
	return daggerCallAt("", args...)
}

func daggerCallAt(modPath string, args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		execArgs := []string{"dagger", "call"}
		if modPath != "" {
			execArgs = append(execArgs, "-m", modPath)
		}
		return c.WithExec(append(execArgs, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerFunctions(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "functions"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

// fileContents is syntax sugar for Container.WithNewFile.
func fileContents(path, contents string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithNewFile(path, heredoc.Doc(contents))
	}
}

func configFile(dirPath string, cfg *modules.ModuleConfig) dagger.WithContainerFunc {
	cfgPath := filepath.Join(dirPath, "dagger.json")
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return fileContents(cfgPath, string(cfgBytes))
}

func privateRepoSetup(c *dagger.Client, t *testctx.T, tc vcsTestCase) (dagger.WithContainerFunc, func()) {
	var socket *dagger.Socket
	cleanup := func() {}
	if tc.sshKey {
		var sockPath string
		sockPath, cleanup = setupPrivateRepoSSHAgent(t)
		socket = c.Host().UnixSocket(sockPath)
	}

	return func(ctr *dagger.Container) *dagger.Container {
		if socket != nil {
			ctr = ctr.
				WithUnixSocket("/sock/unix-socket", socket).
				WithEnvVariable("SSH_AUTH_SOCK", "/sock/unix-socket")
		}
		if token := tc.token(); token != "" {
			ctr = ctr.
				WithNewFile("/tmp/git-config", makeGitCredentials("https://"+tc.expectedHost, "git", token)).
				WithEnvVariable("GIT_CONFIG_GLOBAL", "/tmp/git-config")
		}

		return ctr
	}, cleanup
}

func makeGitCredentials(url string, username string, token string) string {
	helper := fmt.Sprintf(`!f() { test "$1" = get && echo -e "password=%s\nusername=%s"; }; f`, token, username)

	contents := bytes.NewBuffer(nil)
	fmt.Fprintf(contents, "[credential %q]\n", url)
	fmt.Fprintf(contents, "\thelper = %q\n", helper)
	return contents.String()
}

func hostDaggerCommandRaw(ctx context.Context, t testing.TB, workdir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(daggerCliPath(t), args...)
	cleanupExec(t, cmd)
	cmd.Env = append(os.Environ(), telemetry.PropagationEnv(ctx)...)
	cmd.Dir = workdir
	return cmd
}

func hostDaggerExecRaw(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := hostDaggerCommandRaw(ctx, t, workdir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s: %w", string(output), err)
	}
	return output, err
}

func hostDaggerModuleExec(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) {
	t.Helper()
	return hostDaggerExecRaw(ctx, t, workdir, append([]string{"module"}, args...)...)
}

func cleanupExec(t testing.TB, cmd *exec.Cmd) {
	t.Cleanup(func() {
		if cmd.Process == nil {
			t.Logf("never started: %v", cmd.Args)
			return
		}
		done := make(chan struct{})
		go func() {
			cmd.Wait()
			close(done)
		}()

		signals := []syscall.Signal{
			syscall.SIGINT,
			syscall.SIGTERM,
			syscall.SIGKILL,
		}
		doSignal := func() {
			if len(signals) == 0 {
				return
			}
			var signal syscall.Signal
			signal, signals = signals[0], signals[1:]
			t.Logf("sending %s: %v", signal, cmd.Args)
			cmd.Process.Signal(signal)
		}
		doSignal()

		for {
			select {
			case <-done:
				t.Logf("exited: %v", cmd.Args)
				return
			case <-time.After(30 * time.Second):
				if !t.Failed() {
					t.Errorf("process did not exit immediately")
				}

				doSignal()
			}
		}
	})
}

func sdkSource(sdk, contents string) dagger.WithContainerFunc {
	return fileContents(sdkSourceFile(sdk), contents)
}

func sdkSourceAt(dir, sdk, contents string) dagger.WithContainerFunc {
	path := sdkSourceFile(sdk)
	if sdk == "python" && dir != "." && dir != "test" {
		path = strings.ReplaceAll(path, "test", dir)
	}
	return fileContents(filepath.Join(dir, path), contents)
}

func sdkSourceFile(sdk string) string {
	switch sdk {
	case "go":
		return "main.go"
	case "python":
		return "src/test/__init__.py"
	case "typescript":
		return "src/index.ts"
	case "java", "./sdk/java":
		return "src/main/java/io/dagger/modules/test/Test.java"
	default:
		panic(fmt.Errorf("unknown sdk %q", sdk))
	}
}

func sdkCodegenFile(t *testctx.T, sdk string) string {
	t.Helper()
	switch sdk {
	case "go":
		// FIXME: go codegen is split up into dagger/dagger.gen.go and
		// dagger/internal/dagger/dagger.gen.go
		return "internal/dagger/dagger.gen.go"
	case "python":
		return "sdk/src/dagger/client/gen.py"
	case "typescript":
		return "sdk/client.gen.ts"
	default:
		panic(fmt.Errorf("unknown sdk %q", sdk))
	}
}

func modInit(t *testctx.T, c *dagger.Client, sdk, contents string, extra ...string) *dagger.Container {
	t.Helper()
	return goGitBase(t, c).
		With(func(ctr *dagger.Container) *dagger.Container {
			if sdk == "java" {
				sdkSrc, err := filepath.Abs("../../sdk/java")
				require.NoError(t, err)
				ctr = ctr.WithMountedDirectory("sdk/java", c.Host().Directory(sdkSrc))
				sdk = "./sdk/java"
			}
			return ctr
		}).
		With(withModInit(sdk, contents, extra...))
}

func withModInit(sdk, contents string, extra ...string) dagger.WithContainerFunc {
	return withModInitAt(".", sdk, contents, extra...)
}

func withModInitAt(dir, sdk, contents string, extra ...string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		name := filepath.Base(dir)
		if name == "." {
			name = "test"
		}
		args := []string{"init", "--sdk=" + sdk, "--name=" + name, "--source=" + dir}
		args = append(args, extra...)
		args = append(args, dir)
		ctr = ctr.With(daggerExec(args...))
		if contents != "" {
			return ctr.With(sdkSourceAt(dir, sdk, contents))
		}
		return ctr
	}
}

func currentSchema(ctx context.Context, t *testctx.T, ctr *dagger.Container) *introspection.Schema {
	t.Helper()
	out, err := ctr.With(daggerQuery(introspection.Query)).Stdout(ctx)
	require.NoError(t, err)
	var schemaResp introspection.Response
	err = json.Unmarshal([]byte(out), &schemaResp)
	require.NoError(t, err)
	return schemaResp.Schema
}

var moduleIntrospection = daggerQuery(`
query { host { directory(path: ".") { asModule {
    description
    objects {
        asObject {
            name
            description
            constructor {
                description
                args {
                    name
                    description
                    defaultValue
                }
            }
            functions {
                name
                description
                args {
                    name
                    description
                    defaultValue
                }
            }
            fields {
                name
                description
            }
        }
    }
	interfaces {
	    asInterface {
			name
			description
			functions {
				name
				description
				args {
					name
					description
				}
            }
        }
    }
    enums {
        asEnum {
            name
            description
            members {
                name
				value
				description
			}
        }
    }
} } } }
`)

func inspectModule(ctx context.Context, t *testctx.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	out, err := ctr.With(moduleIntrospection).Stdout(ctx)
	require.NoError(t, err)
	result := gjson.Get(out, "host.directory.asModule")
	t.Logf("module introspection:\n%v", result.Raw)
	return result
}

func inspectModuleObjects(ctx context.Context, t *testctx.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	return inspectModule(ctx, t, ctr).Get("objects.#.asObject")
}

func inspectModuleInterfaces(ctx context.Context, t *testctx.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	return inspectModule(ctx, t, ctr).Get("interfaces.#.asInterface")
}

func goGitBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

func logGen(ctx context.Context, t *testctx.T, modSrc *dagger.Directory) {
	t.Helper()
	generated, err := modSrc.File("dagger.gen.go").Contents(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		t.Name()
		fileName := filepath.Join(
			os.TempDir(),
			t.Name(),
			fmt.Sprintf("dagger.gen.%d.go", time.Now().Unix()),
		)

		if err := os.MkdirAll(filepath.Dir(fileName), 0o755); err != nil {
			t.Logf("failed to create temp dir for generated code: %v", err)
			return
		}

		if err := os.WriteFile(fileName, []byte(generated), 0o644); err != nil {
			t.Logf("failed to write generated code to %s: %v", fileName, err)
		} else {
			t.Logf("wrote generated code to %s", fileName)
		}
	})
}
