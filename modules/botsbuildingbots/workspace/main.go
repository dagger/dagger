package main

import (
	"context"
	"dagger/workspace/internal/dagger"
	"dagger/workspace/internal/telemetry"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type Workspace struct {
	// +private
	Model string

	// +private
	Evals int

	// The authoritative documentation.
	README string

	// The current system prompt.
	SystemPrompt string
}

//go:embed README.md
var README string

//go:embed INITIAL.md
var INITIAL string

func New(
	// +default=""
	model string,
	// +default=2
	evals int,
) *Workspace {
	return &Workspace{
		Model:        model,
		Evals:        evals,
		README:       README,
		SystemPrompt: INITIAL,
		// SystemPrompt: README,
	}
}

// Set the system prompt for future evaluations.
func (w *Workspace) WithSystemPrompt(prompt string) *Workspace {
	w.SystemPrompt = prompt
	return w
}

// Evaluate the LLM and return the history of prompts, responses, and tool calls.
func (w *Workspace) Evaluate(ctx context.Context) (string, error) {
	reports := make(chan string, w.Evals)
	wg := new(sync.WaitGroup)
	var succeeded int
	var toolsDesc string
	for attempt := range w.Evals {
		wg.Add(1)
		go func() {
			defer wg.Done()

			ctx, span := Tracer().Start(ctx, fmt.Sprintf("attempt %d", attempt+1),
				telemetry.Reveal())

			var failed error
			defer telemetry.End(span, func() error { return failed })

			report := new(strings.Builder)
			defer func() { reports <- report.String() }()
			fmt.Fprintf(report, "## Attempt %d\n", attempt+1)
			fmt.Fprintln(report)

			llm, err := w.evaluate(ctx, attempt)
			if err != nil {
				fmt.Fprintln(report, "Evaluation errored:", err)
				failed = err
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
				tools, err := llm.Tools(ctx)
				if err != nil {
					fmt.Fprintln(report, "Failed to get history:", err)
					return
				}
				if len(tools) > len(toolsDesc) {
					toolsDesc = tools
				}
			}
		}()
	}

	finalReport := new(strings.Builder)
	fmt.Fprintln(finalReport, "# All Attempts")
	fmt.Fprintln(finalReport)
	for range w.Evals {
		fmt.Fprintln(finalReport, <-reports)
	}
	fmt.Fprintln(finalReport, "# Tools")
	fmt.Fprintln(finalReport)
	fmt.Fprintf(finalReport, toolsDesc)
	fmt.Fprintln(finalReport)

	fmt.Fprintln(finalReport, "# Final Report")
	fmt.Fprintln(finalReport)
	fmt.Fprintf(finalReport, "SUCCESS RATE: %d/%d (%.f%%)\n", succeeded, w.Evals, float64(succeeded)/float64(w.Evals)*100)

	return finalReport.String(), nil
}

func (w *Workspace) evaluate(ctx context.Context, attempt int) (_ *dagger.LLM, rerr error) {
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
		var exit *dagger.ExecError
		if errors.As(err, &exit) {
			return llm, fmt.Errorf("command failed (probably built with CGo): %w", err)
		}
		return llm, err
	}
	out = strings.TrimSpace(out)
	if out != "0.0.0-dev" {
		return llm, fmt.Errorf("unexpected version: %q", out)
	}
	return llm, nil
}
