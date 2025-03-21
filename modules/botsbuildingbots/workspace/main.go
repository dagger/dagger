package main

import (
	"context"
	"dagger/workspace/internal/dagger"
	"dagger/workspace/internal/telemetry"
	_ "embed"
	"fmt"
	"strings"
	"sync"
)

type Workspace struct {
	// +private
	Model string

	Evals int

	SystemPrompt string
}

//go:embed start.md
var initialPrompt string

func New(
	// +default=""
	model string,
	// +default=2
	evals int,
) *Workspace {
	return &Workspace{
		Model:        model,
		Evals:        evals,
		SystemPrompt: initialPrompt,
	}
}

// Set the system prompt for future evaluations.
func (w *Workspace) ReplaceSystemPrompt(prompt string) *Workspace {
	w.SystemPrompt = prompt
	return w
}

// Evaluate the LLM and return the history of prompts, responses, and tool calls.
func (w *Workspace) Evaluate(ctx context.Context) (string, error) {
	reports := make(chan string, w.Evals)
	wg := new(sync.WaitGroup)
	var succeeded int
	for attempt := range w.Evals {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report := new(strings.Builder)
			defer func() { reports <- report.String() }()
			fmt.Fprintf(report, "## Attempt %d\n", attempt+1)
			fmt.Fprintln(report)

			llm, err := w.evaluate(ctx, attempt)
			if err != nil {
				fmt.Fprintln(report, "Evaluation errored:", err)
			} else {
				succeeded++
			}

			if llm != nil {
				history, err := llm.History(ctx)
				if err != nil {
					fmt.Fprintln(report, "Failed to get history:", err)
					return
				}
				for _, line := range history {
					fmt.Fprintln(report, line)
				}
			}
		}()
	}

	finalReport := new(strings.Builder)
	for range w.Evals {
		fmt.Fprintln(finalReport, <-reports)
	}
	fmt.Fprintln(finalReport, "## Final Report")
	fmt.Fprintln(finalReport)
	fmt.Fprintf(finalReport, "SUCCESS RATE: %d/%d (%.f%%)\n", succeeded, w.Evals, float64(succeeded)/float64(w.Evals)*100)

	return finalReport.String(), nil
}

func (w *Workspace) evaluate(ctx context.Context, attempt int) (_ *dagger.LLM, rerr error) {
	ctx, span := Tracer().Start(ctx, fmt.Sprintf("attempt %d", attempt+1))
	defer telemetry.End(span, func() error { return rerr })
	llm, err := dag.Evals(attempt + 1).
		WithModel(w.Model).
		WithSystemPrompt(w.SystemPrompt).
		BuildMultiLLM().
		Sync(ctx)
	if err != nil {
		return nil, err
	}
	res := llm.File()
	ctr := dag.Container().
		From("alpine").
		WithFile("/bin/booklit", res).
		WithExec([]string{"chmod", "+x", "/bin/booklit"}).
		WithExec([]string{"/bin/booklit", "--version"})
	out, err := ctr.Stdout(ctx)
	if err != nil {
		return llm, err
	}
	out = strings.TrimSpace(out)
	if out != "0.0.0-dev" {
		return llm, fmt.Errorf("unexpected version: %q", out)
	}
	return llm, nil
}
