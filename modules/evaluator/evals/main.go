package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/stretchr/testify/require"
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
		func(t testing.TB, llm *dagger.LLM) {
			reply, err := llm.Env().Output("file").AsFile().Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, reply, "potato")
		})
}

// Test basic prompting.
func (m *Evals) Basic(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 10}).
			WithPrompt("What is 2 + 2? Respond with a single number."),
		func(t testing.TB, llm *dagger.LLM) {
			reply, err := llm.LastReply(ctx)
			require.NoError(t, err)
			require.Contains(t, reply, "4")
		})
}

// Models smart enough to follow instructions like 'do X three times.'
var SmartModels = []string{
	"gpt-4o",
	"gemini-2.0-flash",
	"claude-3-5-haiku-latest",
	"claude-3-5-sonnet-latest",
	"claude-3-7-sonnet-latest",
}

// Test the common workspace pattern.
func (m *Evals) WorkspacePattern(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
			WithEnv(dag.Env().
				WithTestspaceInput("dir", dag.Testspace(m.Attempt), "Your workspace for performing research.").
				WithTestspaceOutput("out", "The workspace containing your findings."),
			).
			WithPrompt(`Research and record three findings.`),
		func(t testing.TB, llm *dagger.LLM) {
			findings, err := llm.Env().Output("out").AsTestspace().Findings(ctx)
			require.NoError(t, err)
			model, err := llm.Model(ctx)
			require.NoError(t, err)
			if slices.Contains(SmartModels, model) {
				require.Len(t, findings, 3)
				all := map[string]int{}
				for _, f := range findings {
					all[f]++
				}
				require.Len(t, all, 3, "all findings should be unique")
			} else {
				// can't expect much from local models atm
				require.NotEmpty(t, findings)
			}
		})
}

// Test that the model is conscious of a "current state" without needing
// explicit prompting.
// func (m *Evals) SingleState(ctx context.Context) (*Report, error) {
// 	return withLLMReport(ctx,
// 		m.LLM().
// 			WithContainer(
// 				dag.Container().
// 					From("alpine").
// 					WithEnvVariable("TERM", "xterm-potato"),
// 			).
// 			WithPrompt("what is the value of the TERM environment variable?"),
// 		func(t testing.TB, llm *dagger.LLM) {
// 			reply, err := llm.LastReply(ctx)
// 			require.NoError(t, err)
// 			require.Contains(t, reply, "xterm-potato")
// 		})
// }

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
	return withLLMReport(ctx,
		m.llm(dagger.LLMOpts{MaxAPICalls: 20}).
			WithEnv(dag.Env().
				WithContainerInput("ctr",
					dag.Container().
						WithEnvVariable("BUSTER", fmt.Sprintf("%d-%s", m.Attempt, time.Now())),
					"A scratch container to start from.")).
			WithPrompt("give me a minimal container for PHP 7 development").
			Loop().
			WithPrompt("now install nano").
			Loop().
			WithPrompt("undo that and install vim instead").
			Loop(),
		func(t testing.TB, llm *dagger.LLM) {
			res := llm.BindResult("_").AsContainer()

			out, err := res.WithExec([]string{"php", "--version"}).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "PHP 7")

			out, err = res.WithExec([]string{"vim", "--version"}).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "VIM - Vi IMproved")

			_, err = res.WithExec([]string{"which", "nano"}, dagger.ContainerWithExecOpts{
				Expect: dagger.ReturnTypeFailure,
			}).Sync(ctx)
			require.NoError(t, err)

			tmp := t.TempDir()
			path, err := res.AsTarball().Export(ctx, filepath.Join(tmp, "image.tar"))
			require.NoError(t, err)

			image, err := tarball.ImageFromPath(path, nil)
			require.NoError(t, err)

			config, err := image.ConfigFile()
			require.NoError(t, err)

			require.NotEmpty(t, config.History)
			for _, layer := range config.History {
				require.NotContains(t, layer.CreatedBy, "nano", "Layer should not contain nano")
			}
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
						"The container to use to build Booklit.").
					WithFileOutput("bin", "The compiled Booklit binary."),
			).
			WithPrompt("Mount $repo into $ctr at /src, set it as your workdir, and build ./cmd/booklit with the CGO_ENABLED env var set to 0."),
		func(t testing.TB, llm *dagger.LLM) {
			BuildMultiAssert(ctx, t, llm)
		})
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
						"The container to use to build Booklit.").
					WithFileOutput("bin", "The compiled Booklit binary."),
			).
			WithPrompt("Mount my repo into the container, set it as your workdir, and build ./cmd/booklit with the CGO_ENABLED env var set to 0.").
			WithPrompt("Return the compiled binary."),
		func(t testing.TB, llm *dagger.LLM) {
			BuildMultiAssert(ctx, t, llm)
		})
}

// Extracted for reuse between BuildMulti tests
func BuildMultiAssert(ctx context.Context, t testing.TB, llm *dagger.LLM) {
	f, err := llm.Env().Output("bin").AsFile().Sync(ctx)
	require.NoError(t, err)

	history, err := llm.History(ctx)
	require.NoError(t, err)
	if !strings.Contains(strings.Join(history, "\n"), "withEnvVariable") {
		t.Error("should have used the withEnvVariable API - use the right tool for the job!")
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
//
// SUCCESS RATE (ballpark):
// - claude-3-7-sonnet-latest: 100%
// - gpt-4o: 100%
// - gemini-2.0-flash: 0%
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
		func(t testing.TB, llm *dagger.LLM) {
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
	Succeeded bool
	Report    string
}

func withLLMReport(
	ctx context.Context,
	llm *dagger.LLM,
	check func(testing.TB, *dagger.LLM),
) (*Report, error) {
	report := new(strings.Builder)

	llm, err := llm.Sync(ctx)
	if err != nil {
		fmt.Fprintln(report, "Evaluation errored:")
		fmt.Fprintln(report)
		fmt.Fprintln(report, err)
		return &Report{
			Succeeded: false,
			Report:    report.String(),
		}, nil
	}

	var succeeded bool
	t := newT(ctx, "eval")
	(func() {
		defer func() {
			x := recover()
			switch x {
			case nil:
			case testSkipped{}, testFailed{}:
			default:
				succeeded = false
				fmt.Fprintln(report, "PANIC:", x)
				report.Write(debug.Stack())
				fmt.Fprintln(report)
			}
		}()
		check(t, llm)
	}())

	finalMessage := "<UNEXPECTED>"
	if t.Failed() {
		finalMessage = "FAILED"
		fmt.Fprintln(report, "Evaluation failed:")
		fmt.Fprintln(report)
		fmt.Fprintln(report, t.Logs())
	} else if t.Skipped() {
		finalMessage = "SKIPPED"
		fmt.Fprintln(report, "Evaluation skipped:")
		fmt.Fprintln(report)
		fmt.Fprintln(report, t.Logs())
	} else {
		finalMessage = "SUCCESS"
		succeeded = true
	}

	history, err := llm.History(ctx)
	if err != nil {
		fmt.Fprintln(report, "Failed to get history:", err)
	} else {
		fmt.Fprintln(report, "<messages>")
		for i, line := range history {
			fmt.Fprintf(report, "%d. %s\n", i+1, line)
		}
		fmt.Fprintln(report, "</messages>")
	}
	inputTokens, err := llm.TokenUsage().InputTokens(ctx)
	if err != nil {
		fmt.Fprintln(report, "Failed to get input tokens:", err)
	}
	outputTokens, err := llm.TokenUsage().OutputTokens(ctx)
	if err != nil {
		fmt.Fprintln(report, "Failed to get output tokens:", err)
	}
	fmt.Fprintln(report)
	fmt.Fprintln(report, "### Total Token Cost")
	fmt.Fprintln(report)
	fmt.Fprintln(report, "* Input Tokens:", inputTokens)
	fmt.Fprintln(report, "* Output Tokens:", outputTokens)
	fmt.Fprintln(report)
	fmt.Fprintln(report, finalMessage)

	return &Report{
		Succeeded: succeeded,
		Report:    report.String(),
	}, nil
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
