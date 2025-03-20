package main

import (
	"context"
	"fmt"
	"strings"
)

type Workspace struct {
	// +private
	Model string

	SystemPrompt string
}

func New(model string) *Workspace {
	return &Workspace{
		Model: model,
		SystemPrompt: `You interact with an immutable GraphQL API by calling tools that return new state objects.
State does not mutate in place; each call produces a new instance, which updates your available tools.`,
	}
}

// Set the system prompt for future evaluations.
func (w *Workspace) ReplaceSystemPrompt(prompt string) *Workspace {
	w.SystemPrompt = prompt
	return w
}

// Evaluate the LLM and return the history of prompts, responses, and tool calls.
func (w *Workspace) Evaluate(ctx context.Context) ([]string, error) {
	llm, err := dag.Evals().
		WithModel(w.Model).
		WithSystemPrompt(w.SystemPrompt).
		BuildMultiLlm().
		Sync(ctx)
	if err != nil {
		return nil, err
	}
	history, err := llm.History(ctx)
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
		return nil, err
	}
	if strings.TrimSpace(out) != "0.0.0-dev" {
		return nil, fmt.Errorf("unexpected version: %q\n\nhistory:\n%s", out, strings.Join(history, "\n"))
	}
	return history, nil
}
