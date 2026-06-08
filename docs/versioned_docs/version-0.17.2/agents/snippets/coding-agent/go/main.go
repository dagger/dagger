package main

import (
	"dagger/coding-agent/internal/dagger"
)

type CodingAgent struct{}

// Write a Go program
func (m *CodingAgent) GoProgram(
	// The programming assignment, e.g. "write me a curl clone"
	assignment string,
) *dagger.Container {
	result := dag.LLM().
		WithToyWorkspace(dag.ToyWorkspace()).
		WithPromptVar("assignment", assignment).
		WithPrompt(`
			You are an expert go programmer. You have access to a workspace.
			Use the default directory in the workspace.
			Do not stop until the code builds.
			Do not use the container.
			Complete the assignment: $assignment
			`).
		ToyWorkspace().
		Container()
	return result
}
