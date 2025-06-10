package main

import (
	"context"
	"crypto/rand"
	"dagger/evals/internal/dagger"
	"dagger/evals/internal/telemetry"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
	"go.opentelemetry.io/otel/attribute"
)

// Models smart enough to follow instructions like 'do X three times.'
var SmartModels = []string{
	"gpt-4o",
	"gpt-4.1",
	"gemini-2.0-flash",
	"claude-3-5-sonnet-latest",
	"claude-3-7-sonnet-latest",
	"claude-sonnet-4-0",
}

type Evals struct {
	Base *dagger.LLM
}

func New(base *dagger.LLM) *Evals {
	return &Evals{
		Base: base,
	}
}

type LifeAlert struct {
	Base *dagger.LLM
}

func (e *LifeAlert) WithBase(base *dagger.LLM) *LifeAlert {
	e.Base = base
	return e
}

func (e *LifeAlert) Name() string {
	return "LifeAlert"
}

func (e *LifeAlert) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithEnv(dag.Env().
			WithDirectoryInput("dir", dag.Directory(), "A directory to write a file into.").
			WithFileOutput("file", "A file containing knowledge you don't have."),
		).
		WithPrompt("Ask me what to write to the file.")
}

func (e *LifeAlert) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		reply, err := prompt.Env().Output("file").AsFile().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.ToLower(reply), "potato")
	})
}

// Test manual intervention allowing the prompt to succeed.
func (m *Evals) LifeAlert() *LifeAlert {
	return &LifeAlert{
		Base: m.Base,
	}
}

type WorkspacePattern struct {
	Base *dagger.LLM
}

func (e *WorkspacePattern) Name() string {
	return "WorkspacePattern"
}

func (e *WorkspacePattern) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithEnv(dag.Env().
			WithWorkspaceInput("dir", dag.Workspace(time.Now().String()),
				"Your workspace for performing research.").
			WithWorkspaceOutput("out",
				"The workspace containing your facts."),
		).
		WithPrompt(`You are a researcher with convenient access to new facts. Research and record three facts. Don't rely on your own knowledge - only rely on the workspace. You can't find a new fact until you've recorded the last one.`)
}

func (e *WorkspacePattern) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		facts, err := prompt.Env().Output("out").AsWorkspace().Facts(ctx)
		require.NoError(t, err)
		model, err := prompt.Model(ctx)
		require.NoError(t, err)
		if slices.Contains(SmartModels, model) {
			require.ElementsMatch(t, []string{
				"The human body has at least five bones.",
				"Most sand is wet.",
				"Go is a programming language for garbage collection.",
			}, facts)
		} else {
			// can't expect much from local models atm
			require.NotEmpty(t, facts)
		}
	})
}

// Test the common workspace pattern.
func (m *Evals) WorkspacePattern() *WorkspacePattern {
	return &WorkspacePattern{
		Base: m.Base,
	}
}

type Basic struct {
	Base *dagger.LLM
}

func (e *Basic) Name() string {
	return "Basic"
}

func (e *Basic) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithPrompt("Hello there! Simply respond with 'potato' and take no other action.")
}

func (e *Basic) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		reply, err := prompt.LastReply(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.ToLower(reply), "potato")
	})
}

// Test basic prompting.
func (m *Evals) Basic() *Basic {
	return &Basic{
		Base: m.Base,
	}
}

type CoreAPI struct {
	Base *dagger.LLM
}

func (e *CoreAPI) Name() string {
	return "CoreAPI"
}

func (e *CoreAPI) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithEnv(dag.Env(dagger.EnvOpts{Privileged: true}).
			WithFileOutput("starch", "A file containing the word potato")).
		WithPrompt("Create a file that contains the word potato, and return it.")
}

func (e *CoreAPI) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		reply, err := prompt.Env().Output("starch").AsFile().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, reply, "potato")
	})
}

// Test that the model is conscious of a "current state" without needing
// explicit prompting.
func (m *Evals) CoreAPI() *CoreAPI {
	return &CoreAPI{
		Base: m.Base,
	}
}

type ModuleDependencies struct {
	Base *dagger.LLM
}

func (e *ModuleDependencies) Name() string {
	return "ModuleDependencies"
}

func (e *ModuleDependencies) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithEnv(dag.Env(dagger.EnvOpts{Privileged: true}).
			WithStringOutput("methods", "The list of methods that you can see.")).
		WithPrompt("List all of the methods that you can see.")
}

func (e *ModuleDependencies) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		reply, err := prompt.Env().Output("methods").AsString(ctx)
		require.NoError(t, err)
		require.Contains(t, reply, "llmTestModule")
		require.Contains(t, reply, "llmDirModuleDepender")
	})
}

// Test that the model is conscious of a "current state" without needing
// explicit prompting.
func (m *Evals) ModuleDependencies(ctx context.Context) (*ModuleDependencies, error) {
	err := dag.ModuleSource("github.com/dagger/dagger-test-modules/llm-dir-module-depender").AsModule().Serve(ctx, dagger.ModuleServeOpts{
		IncludeDependencies: true,
	})
	if err != nil {
		return nil, err
	}
	return &ModuleDependencies{
		Base: m.Base,
	}, nil
}

// func (m *Evals) CoreMulti(ctx context.Context) (*Report, error) {
// 	return withLLMReport(ctx,
// 		m.LLM().
// 			WithEnv(dag.Env(dagger.EnvOpts{Privileged: true}).
// 				WithContainerOutput("mounted", "The container with the mounted directory")).
// 			WithPrompt("Create a directory with a file named 'foo.txt'.").
// 			WithPrompt("Create a scratch container and mount the directory at /src."),
// 		func(t testing.TB, llm *dagger.LLM) {
// 			_, err := llm.Env().Output("mounted").AsContainer().File("/src/foo.txt").Contents(ctx)
// 			require.NoError(t, err)
// 		})
// }

type UndoChanges struct {
	Base *dagger.LLM
}

func (e *UndoChanges) Name() string {
	return "UndoChanges"
}

func (e *UndoChanges) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithEnv(dag.Env().
			WithDirectoryInput("dir", dag.Directory(),
				"A directory in which to write files.").
			WithDirectoryOutput("out", "The directory with the desired contents.")).
		WithPrompt("Create the file /a with contents 1.").
		Loop().
		WithPrompt("Create the file /b with contents 2.").
		Loop().
		WithPrompt("Nevermind - go back to just /a and create /c with contents 3, and return that.")
}

func (e *UndoChanges) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		entries, err := prompt.Env().Output("out").AsDirectory().Entries(ctx)
		require.NoError(t, err)
		sort.Strings(entries)
		require.ElementsMatch(t, []string{"a", "c"}, entries)
	})
}

// Test the model's eagerness to switch to prior states instead of mutating the
// current state to undo past actions.
func (m *Evals) UndoChanges() *UndoChanges {
	return &UndoChanges{
		Base: m.Base,
	}
}

type BuildMulti struct {
	Base *dagger.LLM
}

func (e *BuildMulti) Name() string {
	return "BuildMulti"
}

func (e *BuildMulti) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithEnv(
			dag.Env().
				WithDirectoryInput("repo",
					dag.Git("https://github.com/vito/booklit").Head().Tree(),
					"The Booklit repository.").
				WithContainerInput("ctr",
					dag.Container().
						From("golang").
						WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
						WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
						WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
						WithEnvVariable("GOCACHE", "/go/build-cache").
						WithEnvVariable("BUSTER", fmt.Sprintf("%d-%s", attempt, time.Now())),
					"The Go container to use to build Booklit.").
				WithFileOutput("bin", "The /out/booklit binary."),
		).
		WithPrompt("Mount $repo into $ctr at /src, set it as your workdir, and build ./cmd/booklit with the CGO_ENABLED env var set to 0, writing it to /out/booklit.")
}

func (e *BuildMulti) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		buildMultiAssert(ctx, t, prompt)
	})
}

// Test the model's ability to pass objects around to one another and execute a
// series of operations given at once.
func (m *Evals) BuildMulti() *BuildMulti {
	return &BuildMulti{
		Base: m.Base,
	}
}

type BuildMultiNoVar struct {
	Base *dagger.LLM
}

func (e *BuildMultiNoVar) Name() string {
	return "BuildMultiNoVar"
}

func (e *BuildMultiNoVar) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithEnv(
			dag.Env().
				WithDirectoryInput("notRepo", dag.Directory(), "Bait - ignore this.").
				WithDirectoryInput("repo",
					dag.Git("https://github.com/vito/booklit").Head().Tree(),
					"The Booklit repository.").
				WithContainerInput("notCtr", dag.Container(), "Bait - ignore this.").
				WithContainerInput("ctr",
					dag.Container().
						From("golang").
						WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
						WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
						WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
						WithEnvVariable("GOCACHE", "/go/build-cache").
						WithEnvVariable("BUSTER", rand.Text()),
					"The Go container to use to build Booklit.").
				WithFileOutput("bin", "The /out/booklit binary."),
		).
		WithPrompt("Mount my repo into the container, set it as your workdir, and build ./cmd/booklit with the CGO_ENABLED env var set to 0, writing it to /out/booklit.")
}

func (e *BuildMultiNoVar) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		buildMultiAssert(ctx, t, prompt)
	})
}

// BuildMulti is like BuildMulti but without explicitly referencing the relevant
// objects, leaving the LLM to figure it out.
func (m *Evals) BuildMultiNoVar() *BuildMultiNoVar {
	return &BuildMultiNoVar{
		Base: m.Base,
	}
}

// Extracted for reuse between BuildMulti tests
func buildMultiAssert(ctx context.Context, t testing.TB, llm *dagger.LLM) {
	f, err := llm.Env().Output("bin").AsFile().Sync(ctx)
	require.NoError(t, err)

	history, err := llm.History(ctx)
	require.NoError(t, err)
	if !strings.Contains(strings.Join(history, "\n"), "Container.withEnvVariable") {
		t.Error("should have used Container.withEnvVariable - use the right tool for the job!")
	}

	ctr := dag.Container().
		From("alpine").
		WithFile("/bin/booklit", f).
		WithExec([]string{"chmod", "+x", "/bin/booklit"}).
		WithExec([]string{"/bin/booklit", "--version"})
	out, err := ctr.Stdout(ctx)
	require.NoError(t, err, "command failed - did you forget CGO_ENABLED=0?")

	out = strings.TrimSpace(out)
	require.Equal(t, "0.0.0-dev", out)
}

type ReadImplicitVars struct {
	Base      *dagger.LLM
	WeirdText string
}

func (e *ReadImplicitVars) Name() string {
	return "ReadImplicitVars"
}

func (e *ReadImplicitVars) Prompt(attempt int) *dagger.LLM {
	return e.Base.
		Attempt(attempt).
		WithEnv(dag.Env().
			WithStringInput("myContent", e.WeirdText,
				"The content to write.").
			WithStringInput("desiredName", "/weird.txt",
				"The name of the file to write to.").
			WithDirectoryInput("dest", dag.Directory(),
				"The directory in which to write the file.").
			WithDirectoryOutput("out", "The directory containing the written file.")).
		WithPrompt("I gave you some content, a directory, and a filename. Write the content to the specified file in the directory.")
}

func (e *ReadImplicitVars) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		content, err := prompt.Env().
			Output("out").
			AsDirectory().
			File("weird.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, e.WeirdText, content)
	})
}

// Test that the LLM is able to access the content of variables without the user
// having to expand them in the prompt.
func (m *Evals) ReadImplicitVars() *ReadImplicitVars {
	// use some fun formatting here to make sure it doesn't get lost in
	// the shuffle
	//
	// NOTE: an earlier iteration included a trailing line break, but... honestly
	// just don't do that. when it gets that weird, pass in a file instead. it's a
	// similar issue you might run into with passing it around in a shell, which
	// these vars already draw parallels to (and may even be sourced from).
	weirdText := "I'm a strawberry!"
	return &ReadImplicitVars{
		Base:      m.Base,
		WeirdText: weirdText,
	}
}

type Report struct {
	Succeeded          bool
	Report             string
	ToolsDoc           string
	InputTokens        int
	OutputTokens       int
	CachedTokensReads  int
	CachedTokensWrites int
}

func report(
	ctx context.Context,
	llm *dagger.LLM,
	check func(context.Context, testing.TB, *dagger.LLM),
) (*Report, error) {
	reportMD := new(strings.Builder)

	report := &Report{}

	logs := new(strings.Builder)
	t := testctx.New(runt.New(ctx, "eval"),
		oteltest.WithTracing[*runt.T](oteltest.TraceConfig[*runt.T]{
			Attributes: []attribute.KeyValue{
				attribute.Bool(telemetry.UIRevealAttr, true),
			},
		}),
		oteltest.WithLogging[*runt.T](oteltest.LogConfig{
			LoggerProvider: telemetry.LoggerProvider(ctx),
		}),
		func(next testctx.RunFunc[*runt.T]) testctx.RunFunc[*runt.T] {
			return func(ctx context.Context, w *testctx.W[*runt.T]) {
				next(ctx, w.WithLogger(&logger{logs}))
			}
		},
	).WithContext(ctx)

	evaledLlm, evalErr := llm.Sync(ctx)

	t.Run("assert", func(ctx context.Context, t *testctx.W[*runt.T]) {
		// basic check: running the evals succeeded without e.g. hitting API limits
		require.NoError(t, evalErr, "LLM evaluation did not complete")

		// now that we know it didn't error, re-assign
		llm = evaledLlm

		// run eval-specific assertions
		check(ctx, t, llm)

		require.True(t, false)
	})

	fmt.Fprintln(reportMD, "### Message Log")
	fmt.Fprintln(reportMD)
	history, err := llm.History(ctx)
	if err != nil {
		fmt.Fprintln(reportMD, "Failed to get history:", err)
	} else {
		numLines := len(history)
		// Calculate the width needed for the largest line number
		width := len(fmt.Sprintf("%d", numLines))
		for i, line := range history {
			// Format with right-aligned padding, number, separator, and content
			fmt.Fprintf(reportMD, "    %*d | %s\n", width, i+1, line)
		}
	}
	report.InputTokens, err = llm.TokenUsage().InputTokens(ctx)
	if err != nil {
		fmt.Fprintln(reportMD, "Failed to get input tokens:", err)
	}
	report.OutputTokens, err = llm.TokenUsage().OutputTokens(ctx)
	if err != nil {
		fmt.Fprintln(reportMD, "Failed to get output tokens:", err)
	}
	report.CachedTokensReads, err = llm.TokenUsage().CachedTokenReads(ctx)
	if err != nil {
		fmt.Fprintln(reportMD, "Failed to get output tokens:", err)
	}
	report.CachedTokensWrites, err = llm.TokenUsage().CachedTokenWrites(ctx)
	if err != nil {
		fmt.Fprintln(reportMD, "Failed to get output tokens:", err)
	}
	fmt.Fprintln(reportMD)

	fmt.Fprintln(reportMD, "### Total Token Cost")
	fmt.Fprintln(reportMD)
	fmt.Fprintln(reportMD, "* Input Tokens:", report.InputTokens)
	fmt.Fprintln(reportMD, "* Output Tokens:", report.OutputTokens)
	fmt.Fprintln(reportMD, "* Cached Token Reads:", report.CachedTokensReads)
	fmt.Fprintln(reportMD, "* Cached Token Writes:", report.CachedTokensWrites)
	fmt.Fprintln(reportMD)

	fmt.Fprintln(reportMD, "### Evaluation Result")
	fmt.Fprintln(reportMD)
	if t.Failed() {
		fmt.Fprintln(reportMD, logs.String())
		fmt.Fprintln(reportMD, "FAILED")
	} else if t.Skipped() {
		fmt.Fprintln(reportMD, logs.String())
		fmt.Fprintln(reportMD, "SKIPPED")
	} else {
		fmt.Fprintln(reportMD, "SUCCESS")
		report.Succeeded = true
	}

	report.Report = reportMD.String()

	toolsDoc, err := llm.Tools(ctx)
	if err != nil {
		fmt.Fprintln(reportMD, "Failed to get tools:", err)
	}
	report.ToolsDoc = toolsDoc

	return report, nil
}

type logger struct {
	w io.Writer
}

func (l *logger) Log(args ...any) {
	fmt.Fprintln(l.w, args...)
}

func (l *logger) Logf(format string, args ...any) {
	fmt.Fprintf(l.w, format+"\n", args...)
}

func (l *logger) Error(args ...any) {
	fmt.Fprintln(l.w, args...)
}

func (l *logger) Errorf(format string, args ...any) {
	fmt.Fprintf(l.w, format+"\n", args...)
}
