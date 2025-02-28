package main

import (
	"dagger/toy-programmer/internal/dagger"
)

type ToyProgrammer struct{}

// Write a Go program
func (m *ToyProgrammer) GoProgram(assignment string) *dagger.Container {
	// Create a new workspace, using third-party module
	before := dag.ToyWorkspace()
	// Run the agent loop in the workspace
	after := dag.Llm().
		WithToyWorkspace(before).
		WithPromptVar("assignment", assignment).
		WithPrompt(`
You are an expert go programmer. You have access to a workspace.
Use the read, write, build tools to complete the following assignment.
Do not try to access the container directly.
Don't stop until your code builds.

Assignment: $assignment
`).
		ToyWorkspace()
	// Return the modified workspace's container
	return after.Container()
}
