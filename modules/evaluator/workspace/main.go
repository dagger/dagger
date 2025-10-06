// A workspace for managing and running LLM evaluations.
//
// This module provides the core workspace functionality for running evaluations
// against various AI models, managing system prompts, and analyzing results.
//
// It is intended for internal use within the Evaluator.
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

	// Whether to disable Dagger's built-in system prompt.
	DisableDefaultSystemPrompt bool

	// Evaluations to perform.
	Evals []Eval

	// Observations made throughout running evaluations.
	Findings []string
}

// Eval represents a single evaluation that can be run against an LLM.
//
// Implementations must provide a name, a method to generate a prompt,
// and a check function to validate the LLM's response.
type Eval interface {
	Name(context.Context) (string, error)
	Prompt(base *dagger.LLM) *dagger.LLM
	Check(ctx context.Context, prompt *dagger.LLM) error

	DaggerObject
}

var testedModels = []string{
	// "gpt-4o",
	"gpt-4.1",
	// "qwen2.5-coder:14b",
	"gemini-2.0-flash",
	"claude-sonnet-4-5",
}

// Set the system prompt for future evaluations.
func (w *Workspace) WithoutDefaultSystemPrompt() *Workspace {
	w.DisableDefaultSystemPrompt = true
	return w
}

// Set the system prompt for future evaluations.
func (w *Workspace) WithSystemPrompt(
	// The system prompt to use for evaluations.
	prompt string,
) *Workspace {
	w.SystemPrompt = prompt
	return w
}

// Set the system prompt for future evaluations.
func (w *Workspace) WithSystemPromptFile(
	ctx context.Context,
	// The file containing the system prompt to use.
	file *dagger.File,
) (*Workspace, error) {
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
func (w *Workspace) Backoff(
	// Number of seconds to sleep.
	seconds int,
) *Workspace {
	time.Sleep(time.Duration(seconds) * time.Second)
	return w
}

// Register an eval to perform.
func (w *Workspace) WithEval(
	// The evaluation to add to the workspace.
	eval Eval,
) *Workspace {
	w.Evals = append(w.Evals, eval)
	return w
}

// Register evals to perform.
func (w *Workspace) WithEvals(
	// The list of evaluations to add to the workspace.
	evals []Eval,
) *Workspace {
	w.Evals = append(w.Evals, evals...)
	return w
}

// The list of possible evals you can run.
func (w *Workspace) EvalNames(ctx context.Context) ([]string, error) {
	var names []string
	for _, eval := range w.Evals {
		name, err := eval.Name(ctx)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// The list of models that you can run evaluations against.
func (w *Workspace) KnownModels() []string {
	return testedModels
}

// Record an interesting finding after performing evaluations.
func (w *Workspace) WithFinding(
	// The finding or observation to record.
	finding string,
) *Workspace {
	w.Findings = append(w.Findings, finding)
	return w
}

// defaultAttempts configures a sane(?) default number of attempts to run for
// each provider.
func (*Workspace) defaultAttempts(provider string) int {
	switch strings.ToLower(provider) {
	case "google":
		// Gemini has no token usage limit, just an API rate limit.
		return 3
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

// AttemptsReport contains the aggregated results from multiple evaluation attempts.
type AttemptsReport struct {
	Report            string
	SuccessRate       float64
	SucceededAttempts int
	TotalAttempts     int
	InputTokens       int
	OutputTokens      int
	CachedTokenReads  int
	CachedTokenWrites int
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
	var eval Eval
	for _, e := range w.Evals {
		evalName, err := e.Name(ctx)
		if err != nil {
			return nil, err
		}
		if evalName == name {
			eval = e
			break
		}
	}
	if eval == nil {
		return nil, fmt.Errorf("unknown evaluation: %s", name)
	}

	base := w.baseLLM(dag.LLM(), model)

	if attempts == 0 {
		provider, err := base.Provider(ctx)
		if err != nil {
			return nil, err
		}
		attempts = w.defaultAttempts(provider)
	}

	reports := make([]string, attempts)
	var totalInputTokens, totalOutputTokens int32
	var totalCachedTokenReads, totalCachedTokenWrites int32
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

			prompt := eval.Prompt(base.Attempt(attempt))

			var succeeded bool
			evalErr := eval.Check(ctx, prompt)
			if evalErr == nil {
				succeeded = true
				atomic.AddInt32(&successCount, 1)
			}

			reportMD := new(strings.Builder)
			fmt.Fprintf(reportMD, "## Attempt %d\n", attempt+1)
			fmt.Fprintln(reportMD)

			fmt.Fprintln(reportMD, "### Message Log")
			fmt.Fprintln(reportMD)
			history, err := prompt.History(ctx)
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
			fmt.Fprintln(reportMD)

			fmt.Fprintln(reportMD, "### Total Token Cost")
			fmt.Fprintln(reportMD)
			usage := prompt.TokenUsage()
			if inputTokens, err := usage.InputTokens(ctx); err == nil {
				fmt.Fprintln(reportMD, "* Input Tokens:", inputTokens)
				atomic.AddInt32(&totalInputTokens, int32(inputTokens))
			}
			if outputTokens, err := usage.OutputTokens(ctx); err == nil {
				fmt.Fprintln(reportMD, "* Output Tokens:", outputTokens)
				atomic.AddInt32(&totalOutputTokens, int32(outputTokens))
			}
			if cachedTokenReads, err := usage.CachedTokenReads(ctx); err == nil {
				fmt.Fprintln(reportMD, "* Cached Token Reads:", cachedTokenReads)
				atomic.AddInt32(&totalCachedTokenReads, int32(cachedTokenReads))
			}
			if cachedTokenWrites, err := usage.CachedTokenWrites(ctx); err == nil {
				fmt.Fprintln(reportMD, "* Cached Token Writes:", cachedTokenWrites)
				atomic.AddInt32(&totalCachedTokenWrites, int32(cachedTokenWrites))
			}
			fmt.Fprintln(reportMD)

			fmt.Fprintln(reportMD, "### Evaluation Result")
			fmt.Fprintln(reportMD)
			if evalErr != nil {
				fmt.Fprintln(reportMD, evalErr)
				fmt.Fprintln(reportMD, "FAILED")
			} else {
				fmt.Fprintln(reportMD, "SUCCESS")
			}
			fmt.Fprintln(reportMD)

			reports[attempt] = reportMD.String()

			// Write report to OTel too
			toolsDoc, err := prompt.Tools(ctx)
			if err != nil {
				return err
			}
			// Only print this to OTel, it's too expensive to process with an LLM in the report
			fmt.Fprintln(stdio.Stdout, "## Tools")
			fmt.Fprintln(stdio.Stdout)
			fmt.Fprintln(stdio.Stdout, toolsDoc)
			fmt.Fprintln(stdio.Stdout)
			fmt.Fprint(stdio.Stdout, reportMD.String())

			if !succeeded {
				return errors.New("evaluation failed")
			}

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
		InputTokens:       int(totalInputTokens),
		OutputTokens:      int(totalOutputTokens),
		CachedTokenReads:  int(totalCachedTokenReads),
		CachedTokenWrites: int(totalCachedTokenWrites),
	}, nil
}

// baseLLM configures a base LLM instance with the workspace's settings.
func (w *Workspace) baseLLM(base *dagger.LLM, modelOverride string) *dagger.LLM {
	if base == nil {
		base = dag.LLM()
	}
	if w.DisableDefaultSystemPrompt {
		base = base.WithoutDefaultSystemPrompt()
	}
	if modelOverride == "" {
		modelOverride = w.Model
	}
	if modelOverride != "" {
		base = base.WithModel(modelOverride)
	}
	if w.SystemPrompt != "" {
		base = base.WithSystemPrompt(w.SystemPrompt)
	}
	return base
}
