package main

import (
	"dagger/toy-programmer/internal/dagger"
)

// Write a Go program, then do some light QA
func (m *ToyProgrammer) GoProgramQA(assignment string) *dagger.Container {
	result := m.GoProgram(assignment)
	return dag.Llm().
		WithContainer(result).
		WithPrompt(`
You have a access to a container. There is a go program in the current directory.
Try building it and running it. If possible, figure out what it does and do some light QA.
Then write all your findings to QA.md alongside the program.

Be careful not to wipe the state of the container with a new image. Focus on withExec, file, directory..

Include a table of each command you ran, and the result.
`).Container()
}
