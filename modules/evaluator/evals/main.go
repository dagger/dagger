package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"dagger/evals/internal/telemetry"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
)

type Evals struct {
	Model        string
	Attempt      int
	SystemPrompt string
}

func New() *Evals {
	return &Evals{
		Attempt: 1,
	}
}

func (m *Evals) WithAttempt(attempt int) *Evals {
	m.Attempt = attempt
	return m
}

func (m *Evals) WithModel(model string) *Evals {
	m.Model = model
	return m
}

func (m *Evals) WithSystemPrompt(prompt string) *Evals {
	m.SystemPrompt = prompt
	return m
}

// Test manual intervention allowing the prompt to succeed.
func (m *Evals) LifeAlert(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 10}).
			WithEnv(dag.Env().
				WithDirectoryInput("dir", dag.Directory(), "A directory to write a file into.").
				WithFileOutput("file", "A file containing knowledge you don't have."),
			).
			WithPrompt("Ask me what to write to the file."),
		func(ctx context.Context, t testing.TB, llm *dagger.LLM) {
			reply, err := llm.Env().Output("file").AsFile().Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, strings.ToLower(reply), "potato")
		})
}

// Test basic prompting.
func (m *Evals) Basic(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 5}).
			WithPrompt("Hello there! Simply respond with 'potato' and take no other action."),
		func(ctx context.Context, t testing.TB, llm *dagger.LLM) {
			reply, err := llm.LastReply(ctx)
			require.NoError(t, err)
			require.Contains(t, strings.ToLower(reply), "potato")
		})
}

// Models smart enough to follow instructions like 'do X three times.'
var SmartModels = []string{
	"gpt-4o",
	"gpt-4.1",
	"gemini-2.0-flash",
	"claude-3-5-sonnet-latest",
	"claude-3-7-sonnet-latest",
	"claude-sonnet-4-0",
}

// Test the common workspace pattern.
func (m *Evals) WorkspacePattern(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
			WithEnv(dag.Env().
				WithWorkspaceInput("dir", dag.Workspace(m.Attempt),
					"Your workspace for performing research.").
				WithWorkspaceOutput("out",
					"The workspace containing your facts."),
			).
			WithPrompt(`You are a researcher with convenient access to new facts. Research and record three facts. Don't rely on your own knowledge - only rely on the workspace. You can't find a new fact until you've recorded the last one.`),
		func(ctx context.Context, t testing.TB, llm *dagger.LLM) {
			facts, err := llm.Env().Output("out").AsWorkspace().Facts(ctx)
			require.NoError(t, err)
			model, err := llm.Model(ctx)
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

// Test that the model is conscious of a "current state" without needing
// explicit prompting.
func (m *Evals) CoreAPI(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
			WithEnv(dag.Env(dagger.EnvOpts{Privileged: true}).
				WithFileOutput("starch", "A file containing the word potato")).
			WithPrompt("Create a file that contains the word potato, and return it."),
		func(ctx context.Context, t testing.TB, llm *dagger.LLM) {
			reply, err := llm.Env().Output("starch").AsFile().Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, reply, "potato")
		})
}

// Test that the model is conscious of a "current state" without needing
// explicit prompting.
func (m *Evals) ModuleDependencies(ctx context.Context) (*Report, error) {
	err := dag.ModuleSource("github.com/dagger/dagger-test-modules/llm-dir-module-depender").AsModule().Serve(ctx, dagger.ModuleServeOpts{
		IncludeDependencies: true,
	})
	if err != nil {
		return nil, err
	}
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
			WithEnv(dag.Env(dagger.EnvOpts{Privileged: true}).
				WithStringOutput("methods", "The list of methods that you can see.")).
			WithPrompt("List all of the methods that you can see."),
		func(ctx context.Context, t testing.TB, llm *dagger.LLM) {
			reply, err := llm.Env().Output("methods").AsString(ctx)
			require.NoError(t, err)
			require.Contains(t, reply, "llmTestModule")
			require.Contains(t, reply, "llmDirModuleDepender")
		})
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

// Test the model's eagerness to switch to prior states instead of mutating the
// current state to undo past actions.
func (m *Evals) UndoChanges(ctx context.Context) (*Report, error) {
	env := dag.Env().
		WithDirectoryInput("dir", dag.Directory(),
			"A directory in which to write files.").
		WithDirectoryOutput("out", "The directory with the desired contents.")
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
			WithEnv(env).
			WithPrompt("Create the file /a with contents 1.").
			Loop().
			WithPrompt("Create the file /b with contents 2.").
			Loop().
			WithPrompt("Nevermind - go back to just /a and create /c with contents 3, and return that."),
		func(ctx context.Context, t testing.TB, llm *dagger.LLM) {
			entries, err := llm.Env().Output("out").AsDirectory().Entries(ctx)
			require.NoError(t, err)
			sort.Strings(entries)
			require.ElementsMatch(t, []string{"a", "c"}, entries)
		})
}

// Test the model's ability to pass objects around to one another and execute a
// series of operations given at once.
func (m *Evals) BuildMulti(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
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
							WithEnvVariable("BUSTER", fmt.Sprintf("%d-%s", m.Attempt, time.Now())),
						"The Go container to use to build Booklit.").
					WithFileOutput("bin", "The /out/booklit binary."),
			).
			WithPrompt("Mount $repo into $ctr at /src, set it as your workdir, and build ./cmd/booklit with the CGO_ENABLED env var set to 0, writing it to /out/booklit."),
		buildMultiAssert)
}

// BuildMulti is like BuildMulti but without explicitly referencing the relevant
// objects, leaving the LLM to figure it out.
func (m *Evals) BuildMultiNoVar(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
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
							WithEnvVariable("BUSTER", fmt.Sprintf("%d-%s", m.Attempt, time.Now())),
						"The Go container to use to build Booklit.").
					WithFileOutput("bin", "The /out/booklit binary."),
			).
			WithPrompt("Mount my repo into the container, set it as your workdir, and build ./cmd/booklit with the CGO_ENABLED env var set to 0, writing it to /out/booklit."),
		buildMultiAssert)
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

// Test that the LLM is able to access the content of variables without the user
// having to expand them in the prompt.
func (m *Evals) ReadImplicitVars(ctx context.Context) (*Report, error) {
	// use some fun formatting here to make sure it doesn't get lost in
	// the shuffle
	//
	// NOTE: an earlier iteration included a trailing line break, but... honestly
	// just don't do that. when it gets that weird, pass in a file instead. it's a
	// similar issue you might run into with passing it around in a shell, which
	// these vars already draw parallels to (and may even be sourced from).
	weirdText := "I'm a strawberry!"
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
			WithEnv(dag.Env().
				WithStringInput("myContent", weirdText,
					"The content to write.").
				WithStringInput("desiredName", "/weird.txt",
					"The name of the file to write to.").
				WithDirectoryInput("dest", dag.Directory(),
					"The directory in which to write the file.").
				WithDirectoryOutput("out", "The directory containing the written file.")).
			WithPrompt("I gave you some content, a directory, and a filename. Write the content to the specified file in the directory."),
		func(ctx context.Context, t testing.TB, llm *dagger.LLM) {
			content, err := llm.Env().
				Output("out").
				AsDirectory().
				File("weird.txt").
				Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, weirdText, content)
		})
}

func (m *Evals) llm(opts ...dagger.LLMOpts) *dagger.LLM {
	opts = append(opts, dagger.LLMOpts{
		Model: m.Model,
	})
	llm := dag.LLM(opts...)
	if m.SystemPrompt != "" {
		llm = llm.WithSystemPrompt(m.SystemPrompt)
	}
	if m.Attempt > 0 {
		llm = llm.Attempt(m.Attempt)
	}
	return llm
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

func withLLMReport(
	ctx context.Context,
	llm *dagger.LLM,
	check func(context.Context, testing.TB, *dagger.LLM),
) (*Report, error) {
	reportMD := new(strings.Builder)

	report := &Report{}

	t := newT(ctx, "eval")

	evaledLlm, evalErr := llm.Sync(ctx)
	(func() {
		// demarcate assertions from the eval
		ctx, span := Tracer().Start(ctx, "assert", telemetry.Reveal())
		defer func() {
			if t.Failed() {
				span.SetStatus(codes.Error, "assertions failed")
			}
			span.End()
		}()

		// capture test panics, from assertions, skips, or otherwise
		defer func() {
			x := recover()
			switch x {
			case nil:
			case testSkipped{}, testFailed{}:
			default:
				fmt.Fprintln(reportMD, "PANIC:", x)
				reportMD.Write(debug.Stack())
				fmt.Fprintln(reportMD)
			}
		}()

		// basic check: running the evals succeeded without e.g. hitting API limits
		require.NoError(t, evalErr, "LLM evaluation did not complete")

		// now that we know it didn't error, re-assign
		llm = evaledLlm

		// run eval-specific assertions
		check(ctx, t, llm)
	}())

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
		fmt.Fprintln(reportMD, t.Logs())
		fmt.Fprintln(reportMD, "FAILED")
	} else if t.Skipped() {
		fmt.Fprintln(reportMD, t.Logs())
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

type evalT struct {
	*testing.T
	name    string
	ctx     context.Context
	logs    *strings.Builder
	failed  bool
	skipped bool
}

var _ testing.TB = (*evalT)(nil)

func newT(ctx context.Context, name string) *evalT {
	return &evalT{
		T:    &testing.T{}, // unused, has to be here because private()
		name: name,
		ctx:  ctx,
		logs: &strings.Builder{},
	}
}

func (e *evalT) Name() string {
	return e.name
}

func (e *evalT) Helper() {}

func (e *evalT) Logs() string {
	return e.logs.String()
}

func (e *evalT) Context() context.Context {
	return e.ctx
}

func (e *evalT) Error(args ...interface{}) {
	e.Log(args...)
	e.Fail()
}

func (e *evalT) Errorf(format string, args ...interface{}) {
	e.Logf(format, args...)
	e.Fail()
}

func (e *evalT) Log(args ...interface{}) {
	fmt.Fprintln(e.logs, args...)
}

func (e *evalT) Logf(format string, args ...interface{}) {
	fmt.Fprintf(e.logs, format+"\n", args...)
}

func (e *evalT) Fatal(args ...interface{}) {
	e.Log(args...)
	e.FailNow()
}

func (e *evalT) Fatalf(format string, args ...interface{}) {
	e.Logf(format, args...)
	e.FailNow()
}

func (e *evalT) Fail() {
	e.failed = true
}

type testFailed struct{}
type testSkipped struct{}

func (e *evalT) FailNow() {
	e.failed = true
	panic(testFailed{})
}

func (e *evalT) Failed() bool {
	return e.failed
}

func (e *evalT) TempDir() string {
	// Create temporary directory for test
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("evalT-%d", time.Now().UnixNano()))
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		e.Fatal(err)
	}
	return dir
}

func (e *evalT) Chdir(dir string) {
	err := os.Chdir(dir)
	if err != nil {
		e.Fatal(err)
	}
}

func (e *evalT) Cleanup(func()) {}

func (e *evalT) Setenv(key, value string) {
	err := os.Setenv(key, value)
	if err != nil {
		e.Fatal(err)
	}
}

func (e *evalT) Skip(args ...interface{}) {
	e.Log(args...)
	e.SkipNow()
}

func (e *evalT) Skipf(format string, args ...interface{}) {
	e.Logf(format, args...)
	e.SkipNow()
}

func (e *evalT) SkipNow() {
	e.skipped = true
	panic(testSkipped{})
}

func (e *evalT) Skipped() bool {
	return e.skipped
}

func (e *evalT) Deadline() (time.Time, bool) {
	deadline, ok := e.ctx.Deadline()
	return deadline, ok
}
