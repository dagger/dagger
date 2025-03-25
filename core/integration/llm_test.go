package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
	"github.com/creack/pty"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/golden"
)

type LLMSuite struct{}

func TestLLM(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(LLMSuite{})
}

type LLMTestCase struct {
	Ref   string
	Name  string
	Flags []LLMTestCaseFlag
}

type LLMTestCaseFlag struct {
	Key      string
	Value    string
	Optional bool
}

func (flag LLMTestCaseFlag) ToCall() []string {
	return []string{"--" + flag.Key, flag.Value}
}

func (flag LLMTestCaseFlag) ToShell() []string {
	if flag.Optional {
		return []string{"--" + flag.Key, strconv.Quote(flag.Value)}
	}
	return []string{strconv.Quote(flag.Value)}
}

func (LLMSuite) TestCase(ctx context.Context, t *testctx.T) {
	tcs := []LLMTestCase{
		{
			Name: "hello-world",
			Ref:  "./llmtest/go-programmer/",
			Flags: []LLMTestCaseFlag{
				{
					Key:   "assignment",
					Value: "write a hello world program",
				},
			},
		},
	}
	for _, tc := range tcs {
		t.Run(tc.Name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			srcPath, err := filepath.Abs(tc.Ref)
			require.NoError(t, err)
			ctr := goGitBase(t, c).
				WithWorkdir("/work").
				WithMountedDirectory(".", c.Host().Directory(srcPath))

			var flags []string
			for _, flag := range tc.Flags {
				flags = append(flags, flag.ToCall()...)
			}

			recording := fmt.Sprintf("llmtest/%s.golden", tc.Name)
			if golden.FlagUpdate() {
				out, err := ctr.
					With(daggerForwardSecrets(c)).
					With(daggerCall(append([]string{"save"}, flags...)...)).
					Stdout(ctx)
				require.NoError(t, err)

				if dir := filepath.Dir(recording); dir != "." {
					err := os.MkdirAll(dir, 0755)
					require.NoError(t, err)
				}
				err = os.WriteFile(recording, []byte(out), 0644)
				require.NoError(t, err)
			}

			replayData, err := os.ReadFile(recording)
			require.NoError(t, err)
			model := "replay/" + base64.StdEncoding.EncodeToString(replayData)

			t.Run("call", func(ctx context.Context, t *testctx.T) {
				cmd := []string{"--model=" + model, "run"}
				cmd = append(cmd, flags...)
				cmd = append(cmd, "file", "--path=main.go", "contents")
				out, err := ctr.With(daggerCall(cmd...)).Stdout(ctx)
				require.NoError(t, err)
				testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
			})

			t.Run("shell", func(ctx context.Context, t *testctx.T) {
				var flags []string
				for _, flag := range tc.Flags {
					flags = append(flags, flag.ToShell()...)
				}
				out, err := ctr.
					With(daggerShell(fmt.Sprintf(`. --model="%s" | run %s | file main.go | contents`, model, strings.Join(flags, " ")))).
					Stdout(ctx)
				require.NoError(t, err)
				testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
			})
		})
	}
}

func (LLMSuite) TestAPILimit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctrFn := func(llmFlags string) dagger.WithContainerFunc {
		return daggerShell(fmt.Sprintf(`llm %s | with-container alpine | with-prompt "tell me the value of PATH" | loop | with-prompt "now tell me the value of TERM" | historyJSON`, llmFlags))
	}

	recording := "llmtest/api-limit.golden"
	if golden.FlagUpdate() {
		out, err := daggerCliBase(t, c).
			With(daggerForwardSecrets(c)).
			With(ctrFn("")).
			Stdout(ctx)
		require.NoError(t, err)

		if dir := filepath.Dir(recording); dir != "." {
			err := os.MkdirAll(dir, 0755)
			require.NoError(t, err)
		}
		err = os.WriteFile(recording, []byte(out), 0644)
		require.NoError(t, err)
	}

	replayData, err := os.ReadFile(recording)
	require.NoError(t, err)
	llmFlags := fmt.Sprintf("--max-api-calls=1 --model=\"replay/%s\"", base64.StdEncoding.EncodeToString(replayData))

	_, err = daggerCliBase(t, c).
		With(ctrFn(llmFlags)).
		Stdout(ctx)
	requireErrOut(t, err, "reached API call limit: 1")
}

func (LLMSuite) TestAllowLLM(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// llm-test-module passes a prompt to LLM and sets a random string variable to bust cache
	directCallModuleRef := "github.com/dagger/dagger-test-modules/llm-dir-module-depender/llm-test-module"
	// llm-dir-module-depender depends on directCall module via a relative path
	dependerModuleRef := "github.com/dagger/dagger-test-modules/llm-dir-module-depender"

	recording := "llmtest/allow-llm.golden"
	if golden.FlagUpdate() {
		out, err := daggerCliBase(t, c).
			With(daggerForwardSecrets(c)).
			// shared recording amongst subtests, they all do basically the same thing
			With(daggerCall("-m", directCallModuleRef, "--allow-llm=all", "save", "--string-arg", "greet me")).
			Stdout(ctx)
		require.NoError(t, err)
		if dir := filepath.Dir(recording); dir != "." {
			err := os.MkdirAll(dir, 0755)
			require.NoError(t, err)
		}
		err = os.WriteFile(recording, []byte(out), 0644)
		require.NoError(t, err)
	}

	replayData, err := os.ReadFile(recording)
	require.NoError(t, err)
	modelFlag := fmt.Sprintf("--model=replay/%s", base64.StdEncoding.EncodeToString(replayData))

	t.Run("allowed calls", func(ctx context.Context, t *testctx.T) {
		tcs := []struct {
			name     string
			module   string
			allowLLM string
		}{
			{
				name:     "direct allow all",
				module:   directCallModuleRef,
				allowLLM: "all",
			},
			{
				name:     "direct allow specific module",
				module:   directCallModuleRef,
				allowLLM: directCallModuleRef,
			},
			{
				name:     "depender allow all",
				module:   dependerModuleRef,
				allowLLM: "all",
			},
			{
				name:     "depender allow specific module",
				module:   dependerModuleRef,
				allowLLM: directCallModuleRef,
			},
			// we only test various permutations of remote module LLM use, local modules don't require the flag and that's covered by the toy-programmer case
		}

		for _, tc := range tcs {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				args := []string{"--allow-llm", tc.allowLLM, modelFlag, "save", "--string-arg", "greet me"}

				_, err := daggerCliBase(t, c).
					With(daggerCallAt(tc.module, args...)).
					Stdout(ctx)
				require.NoError(t, err)
			})
		}
	})

	t.Run("noninteractive prompt fail", func(ctx context.Context, t *testctx.T) {
		args := []string{modelFlag, "save", "--string-arg", "greet me"}

		_, err := daggerCliBase(t, c).
			With(daggerCallAt(directCallModuleRef, args...)).
			Stdout(ctx)
		require.Error(t, err)
	})

	t.Run("environment variable", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithEnvVariable("DAGGER_ALLOW_LLM", "all").
			With(daggerCallAt(dependerModuleRef, modelFlag, "save", "--string-arg", "greet me")).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("shell allow all", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithExec([]string{"dagger", "-m", dependerModuleRef, "--allow-llm=all"}, dagger.ContainerWithExecOpts{
				Stdin:                         fmt.Sprintf(`. %s | save "greet me"`, modelFlag),
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("shell interactive module loads", func(ctx context.Context, t *testctx.T) {
		_, err := daggerCliBase(t, c).
			WithExec([]string{"dagger", "--allow-llm", directCallModuleRef}, dagger.ContainerWithExecOpts{
				Stdin:                         fmt.Sprintf(`%s %s | save "greet me"`, dependerModuleRef, modelFlag),
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("prompt calls", func(ctx context.Context, t *testctx.T) {
		consoleDagger := func(args ...string) (*exec.Cmd, *tuiConsole) {
			t.Helper()
			console, err := newTUIConsole(t, 60*time.Second)
			require.NoError(t, err)

			tty := console.Tty()
			err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 60}) // for plain, we should make this wider, like 150
			require.NoError(t, err)

			cmd := hostDaggerCommand(
				ctx,
				t,
				t.TempDir(),
				args...,
			)
			cmd.Stdin = tty
			cmd.Stdout = tty
			cmd.Stderr = tty

			return cmd, console
		}

		tcs := []struct {
			name     string
			allowLLM string
			module   string
			plain    bool
		}{
			{
				name:     "direct remote module call",
				allowLLM: "",
				module:   directCallModuleRef,
			},
			// TODO: find a way to test plain tui.
			// under test, it doesn't acknowledge input, but works fine irl
			// {
			// 	name:     "plain tui direct remote module call",
			// 	allowLLM: "",
			// 	module:   directCallModuleRef,
			// 	plain:    true,
			// },
			{
				name:     "allowed unrelated, calling direct",
				allowLLM: "github.com/dagger/dagger",
				module:   directCallModuleRef,
			},
			{
				name:     "allowed depender, calling direct",
				allowLLM: dependerModuleRef,
				module:   directCallModuleRef,
			},
			{
				// this should prompt for the dependency
				name:     "allowed depender, calling depender",
				allowLLM: dependerModuleRef,
				module:   dependerModuleRef,
			},
		}

		for _, tc := range tcs {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				progressFlag := "--progress=auto"
				if tc.plain {
					progressFlag = "--progress=plain"
				}
				cmd, console := consoleDagger(
					progressFlag, "call", "-m", tc.module, "--allow-llm", tc.allowLLM, modelFlag, "save", "--string-arg", "greet me",
				)
				defer console.Close()

				err := cmd.Start()
				require.NoError(t, err)

				_, err = console.ExpectString("attempted to access the LLM API. Allow it?")
				require.NoError(t, err)

				// only test the  "no" case- the yes case persists history and requires special handling
				_, err = console.SendLine("n")
				require.NoError(t, err)

				_, err = console.ExpectString("was denied LLM access")
				require.NoError(t, err)

				go console.ExpectEOF()

				err = cmd.Wait()
				require.Error(t, err)
			})
		}
	})
}

func testGoProgram(ctx context.Context, t *testctx.T, c *dagger.Client, program *dagger.File, re any) {
	name, err := program.Name(ctx)
	require.NoError(t, err)
	out, err := goGitBase(t, c).
		WithWorkdir("/src").
		WithMountedFile(name, program).
		WithExec([]string{"go", "run", name}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Regexp(t, re, out)
}

func daggerForwardSecrets(dag *dagger.Client) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithMountedSecret(".env", dag.Secret("file:///dagger.env"))
	}

	// 	return func(ctr *dagger.Container) *dagger.Container {
	// 		propagate := func(env string) {
	// 			if v, ok := os.LookupEnv(env); ok {
	// 				ctr = ctr.WithSecretVariable(env, dag.SetSecret(env, v))
	// 			}
	// 		}

	// 		propagate("ANTHROPIC_API_KEY")
	// 		propagate("ANTHROPIC_BASE_URL")
	// 		propagate("ANTHROPIC_MODEL")

	// 		propagate("OPENAI_API_KEY")
	// 		propagate("OPENAI_AZURE_VERSION")
	// 		propagate("OPENAI_BASE_URL")
	// 		propagate("OPENAI_MODEL")

	// 		propagate("GEMINI_API_KEY")
	// 		propagate("GEMINI_BASE_URL")
	// 		propagate("GEMINI_MODEL")

	//		return ctr
	//	}
}
