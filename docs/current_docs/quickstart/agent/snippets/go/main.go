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
		WithToyWorkspaceInput("before", workspace, "these are the tools to complete the task").
		WithStringInput("assignment", assignment, "this is the assignment, complete it").
		WithToyWorkspaceOutput("after", "the ToyWorkspace with the completed assignment")

	return dag.LLM().
		WithEnv(environment).
		WithPrompt(`
			You are an expert go programmer. You have access to a workspace.
			Use the default directory in the workspace.
			Do not stop until the code builds.`).
		Env().Output("after").AsToyWorkspace().Container()
}
