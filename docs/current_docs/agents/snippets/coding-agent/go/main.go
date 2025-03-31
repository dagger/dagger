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
	environment := dag.Env().
		WithToyWorkspaceInput("before", dag.Workspace(), "these are the tools to complete the task").
		WithStringInput("assignment", assignment, "this is the assignment, complete it").
		WantToyWorkspaceOutput("after")

	return dag.LLM().
		WithEnv(environment).
		WithPrompt(`
			You are an expert go programmer. You have access to a workspace.
			Use the default directory in the workspace.
			Do not stop until the code builds.`).
		Env().Output("after").AsWorkspace().Container()
}
