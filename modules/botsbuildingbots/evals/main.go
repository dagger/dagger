package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
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

func New(attempt int) *Evals {
	return &Evals{
		Attempt: attempt,
	}
}

func (m *Evals) WithModel(model string) *Evals {
	m.Model = model
	return m
}

func (m *Evals) WithSystemPrompt(prompt string) *Evals {
	m.SystemPrompt = prompt
	return m
}

func (m *Evals) UndoSingle(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.LLM().
			WithQuery().
			WithPrompt("give me a container for PHP 7 development").
			Loop().
			WithPrompt("now install nano").
			Loop().
			WithPrompt("undo that and install vim instead").
			Loop(),
		func(t testing.TB, llm *dagger.LLM) {
			res := llm.Container()

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

func (m *Evals) BuildMulti(ctx context.Context) (*Report, error) {
	return withLLMReport(ctx,
		m.LLM().
			SetDirectory("repo", dag.Git("https://github.com/vito/booklit").Head().Tree()).
			SetContainer("ctr",
				dag.Container().
					From("golang").
					WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
					WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
					WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
					WithEnvVariable("GOCACHE", "/go/build-cache").
					WithEnvVariable("BUSTER", fmt.Sprintf("%d-%s", m.Attempt, time.Now())),
			).
			WithPrompt("Mount $repo into $ctr, set it as your workdir, and build ./cmd/booklit with CGO_ENABLED=0.").
			WithPrompt("Return the binary as a File."),
		func(t testing.TB, llm *dagger.LLM) {
			f, err := llm.File().Sync(ctx)
			require.NoError(t, err)

			ctr := dag.Container().
				From("alpine").
				WithFile("/bin/booklit", f).
				WithExec([]string{"chmod", "+x", "/bin/booklit"}).
				WithExec([]string{"/bin/booklit", "--version"})
			out, err := ctr.Stdout(ctx)
			require.NoError(t, err, "command failed - did you forget CGO_ENABLED=0?")

			out = strings.TrimSpace(out)
			require.Equal(t, "0.0.0-dev", out)
		})
}

func (m *Evals) LLM() *dagger.LLM {
	llm := dag.LLM(dagger.LLMOpts{
		Model: m.Model,
	})
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
		return nil, err
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
	if t.Failed() {
		fmt.Fprintln(report, "Evaluation failed:")
		fmt.Fprintln(report)
		fmt.Fprintln(report, t.Logs())
	} else if t.Skipped() {
		fmt.Fprintln(report, "Evaluation skipped:")
		fmt.Fprintln(report)
		fmt.Fprintln(report, t.Logs())
	} else {
		succeeded = true
	}

	history, err := llm.History(ctx)
	if err != nil {
		fmt.Fprintln(report, "Failed to get history:", err)
	} else {
		fmt.Fprintln(report, "<messages>")
		for _, line := range history {
			fmt.Fprintln(report, line)
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
