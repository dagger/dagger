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
	workspace := dag.ToyWorkspace()
	environment := dag.Env().
		WithToyWorkspaceInput("before", workspace, "tools to complete the assignment").
		WithStringInput("assignment", assignment, "the assignment to complete").
		WithToyWorkspaceOutput("after", "the completed assignment")

	return dag.LLM().
		WithEnv(environment).
		WithPrompt(`
			You are an expert go programmer. You have access to a workspace.
			Use the default directory in the workspace.
			Do not stop until the code builds.
			Your assignment is: $assignment`).
		Env().
		Output("after").
		AsToyWorkspace().
		Container()
}
