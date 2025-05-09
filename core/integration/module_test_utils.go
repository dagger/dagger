package core

import (
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

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func daggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerNonNestedExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.
			// Don't persist stable client id between runs. this matches the behavior
			// of actual nested execs. Stable client IDs on the filesystem don't work
			// when run inside layered containers that can branch off and run in parallel.
			WithEnvVariable("XDG_STATE_HOME", "/tmp").
			WithMountedTemp("/tmp").
			WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: false,
			})
	}
}

func daggerNonNestedRun(args ...string) dagger.WithContainerFunc {
	args = append([]string{"run"}, args...)

	return daggerNonNestedExec(args...)
}

func daggerClientInstall(generator string) dagger.WithContainerFunc {
	return daggerExec("client", "install", "--generator="+generator, "--dev")
}

func daggerClientInstallAt(generator string, outputDirPath string) dagger.WithContainerFunc {
	return daggerExec("client", "install", "--generator="+generator, "--dev", outputDirPath)
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
				WithExec([]string{
					"git", "config", "--global",
					"credential.https://" + tc.expectedHost + ".helper",
					`!f() { test "$1" = get && echo -e "password=` + token + `\nusername=git"; }; f`,
				})
		}

		return ctr
	}, cleanup
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

// command for a dagger cli call direct on the host
func hostDaggerCommand(ctx context.Context, t testing.TB, workdir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(daggerCliPath(t), args...)
	cleanupExec(t, cmd)
	cmd.Env = append(os.Environ(), telemetry.PropagationEnv(ctx)...)
	cmd.Dir = workdir
	return cmd
}

// runs a dagger cli command directly on the host, rather than in an exec
func hostDaggerExec(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) { //nolint: unparam
	t.Helper()
	cmd := hostDaggerCommand(ctx, t, workdir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s: %w", string(output), err)
	}
	return output, err
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

				// the process *still* isn't dead? try killing it harder.
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

func modInit(t *testctx.T, c *dagger.Client, sdk, contents string) *dagger.Container {
	t.Helper()
	return goGitBase(t, c).With(withModInit(sdk, contents))
}

func withModInit(sdk, contents string) dagger.WithContainerFunc {
	return withModInitAt(".", sdk, contents)
}

func withModInitAt(dir, sdk, contents string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		name := filepath.Base(dir)
		if name == "." {
			name = "test"
		}
		args := []string{"init", "--sdk=" + sdk, "--name=" + name, "--source=" + dir, dir}
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
            values {
                name
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
