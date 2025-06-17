package main

import (
	"context"
	"dagger/workspace/internal/dagger"
	"dagger/workspace/internal/telemetry"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Workspace struct {
	// +private
	Model string

	// The current system prompt.
	SystemPrompt string

	// Observations made throughout running evaluations.
	Findings []string
}

var testedModels = []string{
	// "gpt-4o",
	"gpt-4.1",
	// "qwen2.5-coder:14b",
	"gemini-2.0-flash",
	"claude-sonnet-4-0",
}

type EvalFunc = func(*dagger.Evals) *dagger.EvalsReport

var evals = map[string]EvalFunc{
	"Basic":              (*dagger.Evals).Basic,
	"BuildMulti":         (*dagger.Evals).BuildMulti,
	"BuildMultiNoVar":    (*dagger.Evals).BuildMultiNoVar,
	"WorkspacePattern":   (*dagger.Evals).WorkspacePattern,
	"ReadImplicitVars":   (*dagger.Evals).ReadImplicitVars,
	"UndoChanges":        (*dagger.Evals).UndoChanges,
	"CoreAPI":            (*dagger.Evals).CoreAPI,
	"ModuleDependencies": (*dagger.Evals).ModuleDependencies,
	// "CoreMulti":        (*dagger.Evals).CoreMulti,
}

// Set the system prompt for future evaluations.
func (w *Workspace) WithSystemPrompt(prompt string) *Workspace {
	w.SystemPrompt = prompt
	return w
}

// Set the system prompt for future evaluations.
func (w *Workspace) WithSystemPromptFile(ctx context.Context, file *dagger.File) (*Workspace, error) {
	content, err := file.Contents(ctx)
	if err != nil {
		return nil, err
	}
	w.SystemPrompt = content
	return w, nil
}

// Backoff sleeps for the given duration in seconds.
//
// Use this if you're getting rate limited and have nothing better to do.
func (w *Workspace) Backoff(seconds int) *Workspace {
	time.Sleep(time.Duration(seconds) * time.Second)
	return w
}

// The list of possible evals you can run.
func (w *Workspace) EvalNames() []string {
	var names []string
	for eval := range evals {
		names = append(names, eval)
	}
	sort.Strings(names)
	return names
}

// The list of models that you can run evaluations against.
func (w *Workspace) KnownModels() []string {
	return testedModels
}

// Record an interesting finding after performing evaluations.
func (w *Workspace) WithFinding(finding string) *Workspace {
	w.Findings = append(w.Findings, finding)
	return w
}

// defaultAttempts configures a sane(?) default number of attempts to run for
// each provider.
func (*Workspace) defaultAttempts(provider string) int {
	switch strings.ToLower(provider) {
	case "google":
		// Gemini has no token usage limit, just an API rate limit.
		return 10
	case "openai":
		// OpenAI is more sensitive to token usage.
		return 5
	case "anthropic":
		// Claude gets overloaded frequently. :(
		return 3
	default:
		// Probably local so don't overload it.
		return 1
	}
}

type AttemptsReport struct {
	Report            string
	SuccessRate       float64
	SucceededAttempts int
	TotalAttempts     int
	InputTokens       int
	OutputTokens      int
}

// Run an evaluation and return its report.
func (w *Workspace) Evaluate(
	ctx context.Context,
	// The evaluation to run. For a list of possible values, call evalNames.
	name string,
	// The model to evaluate.
	// +default=""
	model string,
	// The number of attempts to evaluate across. Has a sane default per-provider.
	// +optional
	attempts int,
) (_ *AttemptsReport, rerr error) {
	evalFn, ok := evals[name]
	if !ok {
		return nil, fmt.Errorf("unknown evaluation: %s", name)
	}

	llm := dag.LLM(dagger.LLMOpts{Model: model})
	if attempts == 0 {
		provider, err := llm.Provider(ctx)
		if err != nil {
			return nil, err
		}
		attempts = w.defaultAttempts(provider)
	}

	reports := make([]string, attempts)
	var inputTokens, outputTokens int32
	var successCount int32
	wg := new(sync.WaitGroup)
	for attempt := range attempts {
		wg.Add(1)
		go func() (rerr error) {
			defer wg.Done()

			ctx, span := Tracer().Start(ctx,
				fmt.Sprintf("%s: attempt %d", name, attempt+1),
				telemetry.Reveal())
			defer telemetry.End(span, func() error { return rerr })
			stdio := telemetry.SpanStdio(ctx, "")
			defer stdio.Close()

			eval := w.evaluate(model, attempt, evalFn)

			evalReport, err := eval.Report(ctx)
			if err != nil {
				return err
			}

			report := new(strings.Builder)
			fmt.Fprintf(report, "## Attempt %d\n", attempt+1)
			fmt.Fprintln(report)
			fmt.Fprintln(report, evalReport)
			reports[attempt] = report.String()

			// Write report to OTel too
			toolsDoc, err := eval.ToolsDoc(ctx)
			if err != nil {
				return err
			}
			// Only print this to OTel, it's too expensive to process with an LLM in the report
			fmt.Fprintln(stdio.Stdout, "## Tools")
			fmt.Fprintln(stdio.Stdout)
			fmt.Fprintln(stdio.Stdout, toolsDoc)
			fmt.Fprintln(stdio.Stdout)
			fmt.Fprint(stdio.Stdout, report.String())

			i, err := eval.InputTokens(ctx)
			if err != nil {
				return err
			}

			o, err := eval.OutputTokens(ctx)
			if err != nil {
				return err
			}

			atomic.AddInt32(&inputTokens, int32(i))
			atomic.AddInt32(&outputTokens, int32(o))

			succeeded, err := eval.Succeeded(ctx)
			if err != nil {
				return err
			}
			if !succeeded {
				return errors.New("evaluation failed")
			}
			atomic.AddInt32(&successCount, 1)
			return nil
		}()
	}

	wg.Wait()

	finalReport := new(strings.Builder)
	fmt.Fprintln(finalReport, "# Model:", model)
	fmt.Fprintln(finalReport)
	fmt.Fprintln(finalReport, "## All Attempts")
	fmt.Fprintln(finalReport)
	for _, report := range reports {
		fmt.Fprint(finalReport, report)
	}

	successRate := float64(successCount) / float64(attempts)
	fmt.Fprintln(finalReport, "## Final Report")
	fmt.Fprintln(finalReport)
	fmt.Fprintf(finalReport, "SUCCESS RATE: %d/%d (%.f%%)\n", successCount, attempts, successRate*100)

	return &AttemptsReport{
		Report:            finalReport.String(),
		SuccessRate:       successRate,
		SucceededAttempts: int(successCount),
		TotalAttempts:     attempts,
		InputTokens:       int(inputTokens),
		OutputTokens:      int(outputTokens),
	}, nil
}

func (w *Workspace) evaluate(model string, attempt int, evalFn EvalFunc) *dagger.EvalsReport {
	return evalFn(
		dag.Evals().
			WithModel(model).
			WithAttempt(attempt + 1).
			WithSystemPrompt(w.SystemPrompt),
	)
}
