package core

// This file contains shared module fixtures, CLI helpers, and introspection
// utilities. It is helper-only and should not own behavior coverage.

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
	fscopy "github.com/dagger/dagger/internal/fsutil/copy"
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
		return c.WithExec(append([]string{"dagger", "api", "functions"}, args...), dagger.ContainerWithExecOpts{
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
	cfgPath := filepath.Join(dirPath, modules.Filename)
	cfgBytes, err := modules.MarshalModuleConfigForFilename(&modules.ModuleConfigWithUserFields{
		ModuleConfig: *cfg,
	}, modules.Filename)
	if err != nil {
		panic(err)
	}
	return fileContents(cfgPath, string(cfgBytes))
}

func testDataPath(t testing.TB, elems ...string) string {
	t.Helper()
	pathElems := append([]string{"testdata"}, elems...)
	absPath, err := filepath.Abs(filepath.Join(pathElems...))
	require.NoError(t, err)
	return absPath
}

func moduleFixture(t testing.TB, c *dagger.Client, fixture string) *dagger.Container {
	t.Helper()
	return goGitBase(t, c).
		With(withModuleFixture(t, c, ".", fixture))
}

func moduleEntrypointFixture(t testing.TB, c *dagger.Client, name, fixture string) *dagger.Container {
	t.Helper()
	return goGitBase(t, c).
		With(withModuleEntrypointFixture(t, c, ".", name, fixture))
}

func withModuleFixture(t testing.TB, c *dagger.Client, dst, fixture string) dagger.WithContainerFunc {
	t.Helper()
	return withTestdataFixture(t, c, dst, "modules", fixture)
}

func withModuleEntrypointFixture(t testing.TB, c *dagger.Client, dst, name, fixture string) dagger.WithContainerFunc {
	t.Helper()
	moduleDir := fixtureJoin(dst, ".dagger/modules/"+name)
	configPath := fixtureJoin(dst, "dagger.toml")
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(withModuleFixture(t, c, moduleDir, fixture)).
			WithNewFile(configPath, fmt.Sprintf(`[modules.%s]
source = ".dagger/modules/%s"
entrypoint = true
`, name, name))
	}
}

func withWorkspaceFixture(t testing.TB, c *dagger.Client, dst, fixture string) dagger.WithContainerFunc {
	t.Helper()
	return withTestdataFixture(t, c, dst, fixture)
}

func fixtureJoin(dst, elem string) string {
	if dst == "" || dst == "." {
		return elem
	}
	return strings.TrimRight(dst, "/") + "/" + elem
}

func withTestdataFixture(t testing.TB, c *dagger.Client, dst string, elems ...string) dagger.WithContainerFunc {
	t.Helper()
	fixturePath := testDataPath(t, elems...)
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithDirectory(dst, c.Host().Directory(fixturePath))
	}
}

func withTestdataFile(t testing.TB, c *dagger.Client, dst string, elems ...string) dagger.WithContainerFunc {
	t.Helper()
	fixturePath := testDataPath(t, elems...)
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithFile(dst, c.Host().Directory(filepath.Dir(fixturePath)).File(filepath.Base(fixturePath)))
	}
}

func copyTestdataFixture(ctx context.Context, t testing.TB, dst string, elems ...string) {
	t.Helper()
	err := fscopy.Copy(ctx, testDataPath(t, elems...), "/", dst, "/")
	require.NoError(t, err)
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
	case "dang":
		return "main.dang"
	case "java", "./sdk/java":
		return "src/main/java/io/dagger/modules/test/Test.java"
	default:
		panic(fmt.Errorf("unknown sdk %q", sdk))
	}
}

func currentSchema(ctx context.Context, t *testctx.T, ctr *dagger.Container) *introspection.Schema {
	t.Helper()
	out, err := ctr.With(daggerQueryAt(".", introspection.Query)).Stdout(ctx)
	require.NoError(t, err)
	var schemaResp introspection.Response
	err = json.Unmarshal([]byte(out), &schemaResp)
	require.NoError(t, err)
	return schemaResp.Schema
}

var moduleIntrospection = daggerQueryAt(".", `
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
