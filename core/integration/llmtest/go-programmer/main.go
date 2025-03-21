package main

import (
	"context"
	"dagger/go-programmer/internal/dagger"
)

type GoProgrammer struct {
	Model string // +private
}

func New(
	model string, // +optional
) *GoProgrammer {
	return &GoProgrammer{model}
}

func (m *GoProgrammer) Run(
	assignment string,
) *dagger.Container {
	return m.llm(assignment).ToyWorkspace().Container()
}

// this is a hack at least until we can return the LLM type to have a better way to save/replay history
func (m *GoProgrammer) Save(
	ctx context.Context,
	assignment string,
) (string, error) {
	return m.llm(assignment).HistoryJSON(ctx)
}

func (m *GoProgrammer) llm(
	assignment string,
) *dagger.LLM {
	return dag.LLM(dagger.LLMOpts{Model: m.Model}).
		WithToyWorkspace(dag.ToyWorkspace()).
		WithPromptVar("assignment", assignment).
		WithPrompt(
			"You are an expert go programmer. You have access to a workspace.\n" +
				"Use the read, write, build tools to complete the following assignment.\n" +
				"Do not try to access the container directly.\n" +
				"Don't stop until your code builds.\n" +
				"\n" +
				"Assignment: $assignment\n",
		)
}
