package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

func (LLMSuite) TestHelloWorld(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	assignment := "write a hello world program"
	src := `
package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Run(
	assignment string,
	model string, // +optional
) *dagger.Container {
	return m.llm(assignment, model).ToyWorkspace().Container()
}

func (m *Test) Save(
	ctx context.Context,
	assignment string,
	model string, // +optional
) (string, error) {
	return m.llm(assignment, model).HistoryJSON(ctx)
}

func (m *Test) llm(
	assignment string,
	model string,
) *dagger.LLM {
	return dag.Llm(dagger.LlmOpts{Model: model}).
		WithToyWorkspace(dag.ToyWorkspace()).
		WithPromptVar("assignment", assignment).
		WithPrompt(
			"You are an expert go programmer. You have access to a workspace.\n" +
			"Use the read, write, build tools to complete the following assignment.\n" +
			"Do not try to access the container directly.\n" +
			"Don't stop until your code builds.\n" +
			"\n" +
			"Assignment: $assignment\n",
		)
}
	`

	modGen := modInit(t, c, "go", src).
		With(withModInitAt("./toy-workspace", "go", toyWorkspaceSrc)).
		With(daggerExec("install", "./toy-workspace"))

	recording := "llmtest/hello-world.golden"

	if golden.FlagUpdate() {
		out, err := modGen.
			With(daggerForwardSecrets(c)).
			With(daggerCall("save", "--assignment="+assignment)).
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
		out, err := modGen.
			With(daggerCall("run", "--assignment="+assignment, "--model="+model, "file", "--path=main.go", "contents")).
			Stdout(ctx)
		require.NoError(t, err)
		testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
	})

	t.Run("shell", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerShell(fmt.Sprintf(`run "%s" --model="%s" | file main.go | contents`, assignment, model))).
			Stdout(ctx)
		require.NoError(t, err)
		testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
	})
}

func (LLMSuite) TestApiLimit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	recording := "llmtest/api-limit.golden"
	replayData, err := os.ReadFile(recording)
	require.NoError(t, err)
	model := "replay/" + base64.StdEncoding.EncodeToString(replayData)

	_, err = daggerCliBase(t, c).
		With(daggerShell(fmt.Sprintf(`llm --max-api-calls=1 --model="%s" | with-container alpine | with-prompt "tell me the value of PATH and TERM in this container using just envVariable"`, model))).
		Stdout(ctx)
	requireErrOut(t, err, "reached API call limit: 1")
}

const toyWorkspaceSrc = `
package main

import (
	"context"
	"dagger/toy-workspace/internal/dagger"
)

// A toy workspace that can edit files and run 'go build'
type ToyWorkspace struct {
	// The workspace's container state.
	// +internal-use-only
	Container *dagger.Container
}

func New() ToyWorkspace {
	return ToyWorkspace{
		// Build a base container optimized for Go development
		Container: dag.Container().
			From("golang").
			WithDefaultTerminalCmd([]string{"/bin/bash"}).
			WithMountedCache("/go/pkg/mod", dag.CacheVolume("go_mod_cache")).
			WithWorkdir("/app").
			WithExec([]string{"go", "mod", "init", "main"}),
	}
}

// Read a file
func (w *ToyWorkspace) Read(ctx context.Context) (string, error) {
	return w.Container.File("main.go").Contents(ctx)
}

// Write a file
func (w ToyWorkspace) Write(content string) ToyWorkspace {
	w.Container = w.Container.WithNewFile("main.go", content)
	return w
}

// Build the code at the current directory in the workspace
func (w *ToyWorkspace) Build(ctx context.Context) error {
	_, err := w.Container.WithExec([]string{"go", "build", "./..."}).Stderr(ctx)
	return err
}
	`

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

	// return func(ctr *dagger.Container) *dagger.Container {
	// 	propagate := func(env string) {
	// 		if v, ok := os.LookupEnv(env); ok {
	// 			ctr = ctr.WithSecretVariable(env, dag.SetSecret(env, v))
	// 		}
	// 	}
	//
	// 	propagate("ANTHROPIC_API_KEY")
	// 	propagate("ANTHROPIC_BASE_URL")
	// 	propagate("ANTHROPIC_MODEL")
	//
	// 	propagate("OPENAI_API_KEY")
	// 	propagate("OPENAI_AZURE_VERSION")
	// 	propagate("OPENAI_BASE_URL")
	// 	propagate("OPENAI_MODEL")
	//
	// 	propagate("GEMINI_API_KEY")
	// 	propagate("GEMINI_BASE_URL")
	// 	propagate("GEMINI_MODEL")
	//
	// 	return ctr
	// }
}
