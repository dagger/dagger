package core

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	_ "embed"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type LLMSuite struct{}

func TestLLM(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(LLMSuite{})
}

//go:embed llm-hello-world.golden
var helloWorldRecording string

//go:embed llm-api-limit.golden
var apiLimitRecording string

func (LLMSuite) TestHelloWorld(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	assignment := "write a hello world program"
	src := `
package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) GoProgram(assignment string) *dagger.Container {
	before := dag.ToyWorkspace()
	after := dag.Llm().
		WithToyWorkspace(before).
		WithPromptVar("assignment", assignment).
		WithPrompt(
			"You are an expert go programmer. You have access to a workspace.\n" +
			"Use the read, write, build tools to complete the following assignment.\n" +
			"Do not try to access the container directly.\n" +
			"Don't stop until your code builds.\n" +
			"\n" +
			"Assignment: $assignment\n",
		).
		ToyWorkspace()
	return after.Container()
}
	`

	modGen := modInit(t, c, "go", src).
		With(withModInitAt("./toy-workspace", "go", toyWorkspaceSrc)).
		With(daggerExec("install", "./toy-workspace"))

	t.Run("dagger call", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(llmReplay(helloWorldRecording)).
			With(daggerCall("go-program", "--assignment="+assignment, "file", "--path=main.go", "contents")).
			Stdout(ctx)
		require.NoError(t, err)
		testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
	})

	t.Run("dagger shell", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(llmReplay(helloWorldRecording)).
			With(daggerShell(fmt.Sprintf("go-program \"%s\" | file main.go | contents", assignment))).
			Stdout(ctx)
		require.NoError(t, err)
		testGoProgram(ctx, t, c, dag.Directory().WithNewFile("main.go", out).File("main.go"), regexp.MustCompile("(?i)hello(.*)world"))
	})
}

func (LLMSuite) TestApiLimit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := daggerCliBase(t, c).
		With(llmReplay(apiLimitRecording)).
		With(daggerShell("llm --max-api-calls=1 | with-container alpine | with-prompt \"tell me the value of PATH and TERM in this container using just envVariable\"")).
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
			WithWorkdir("/app"),
	}
}

// Read a file
func (w *ToyWorkspace) Read(ctx context.Context, path string) (string, error) {
	return w.Container.File(path).Contents(ctx)
}

// Write a file
func (w ToyWorkspace) Write(path, content string) ToyWorkspace {
	w.Container = w.Container.WithNewFile(path, content)
	return w
}

// Build the code at the current directory in the workspace
func (w *ToyWorkspace) Build(ctx context.Context) error {
	_, err := w.Container.WithExec([]string{"go", "build", "./..."}).Stderr(ctx)
	return err
}
	`

func llmReplay(history string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithEnvVariable("LLM_HISTORY_REPLAY", history)
	}
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
