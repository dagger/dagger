package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
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
		return daggerShell(fmt.Sprintf(`llm %s | with-container alpine | with-prompt "tell me the value of PATH and TERM in this container using just envVariable" | historyJSON`, llmFlags))
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

	directCallModuleRef := "github.com/cwlbraa/dagger-test-modules/llm-dir-module-depender/llm-test-module"
	dependerModuleRef := "github.com/cwlbraa/dagger-test-modules/llm-dir-module-depender"

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

	t.Run("direct allow all", func(ctx context.Context, t *testctx.T) {
		_, err = daggerCliBase(t, c).
			With(daggerCallAt(directCallModuleRef, "--allow-llm=all", modelFlag, "save", "--string-arg", "greet me")).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("direct allow specific module", func(ctx context.Context, t *testctx.T) {
		_, err = daggerCliBase(t, c).
			With(daggerCallAt(directCallModuleRef, "--allow-llm", directCallModuleRef, modelFlag, "save", "--string-arg", "greet me")).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("depender allow all", func(ctx context.Context, t *testctx.T) {
		_, err = daggerCliBase(t, c).
			With(daggerCallAt(dependerModuleRef, "--allow-llm=all", modelFlag, "save", "--string-arg", "greet me")).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("depender allow specific module", func(ctx context.Context, t *testctx.T) {
		_, err = daggerCliBase(t, c).
			With(daggerCallAt(dependerModuleRef, "--allow-llm", directCallModuleRef, modelFlag, "save", "--string-arg", "greet me")).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("shell allow all", func(ctx context.Context, t *testctx.T) {
		_, err = daggerCliBase(t, c).
			WithExec([]string{"dagger", "-m", dependerModuleRef, "--allow-llm=all"}, dagger.ContainerWithExecOpts{
				Stdin:                         fmt.Sprintf(`. %s | save "greet me"`, modelFlag),
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("shell interactive module loads", func(ctx context.Context, t *testctx.T) {
		_, err = daggerCliBase(t, c).
			WithExec([]string{"dagger", "--allow-llm", directCallModuleRef}, dagger.ContainerWithExecOpts{
				Stdin:                         fmt.Sprintf(`%s %s | save "greet me"`, dependerModuleRef, modelFlag),
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
	})

	// // TODO, not yet implemented
	// t.Run("environment variable", func(ctx context.Context, t *testctx.T) {
	// 	_, err = daggerCliBase(t, c).
	// 		WithEnvVariable("DAGGER_ALLOW_LLM", "all").
	// 		With(daggerCallAt(dependerModuleRef, modelFlag, "save", "--string-arg", "greet me")).
	// 		Stdout(ctx)
	// 	require.NoError(t, err)
	// })

}

func (LLMSuite) TestPromptAllowLLM(ctx context.Context, t *testctx.T) {
	t.Skip("TODO: these need to use a host CLI so we can futz with TTYs")
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
