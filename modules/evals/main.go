package main

import (
	"dagger/evals/internal/dagger"
)

type Evals struct {
	Model string
}

func (m *Evals) UndoSingle() *dagger.Container {
	return dag.Llm(dagger.LlmOpts{Model: m.Model}).
		WithQuery().
		WithPrompt("give me a container for PHP 7 development").
		Loop().
		WithPrompt("now install nano").
		Loop().
		WithPrompt("undo that and install vim instead").
		Loop().
		Container()
}

func (m *Evals) BuildMulti() *dagger.File {
	return dag.Llm(dagger.LlmOpts{Model: m.Model}).
		SetDirectory("repo", dag.Git("https://github.com/vito/booklit").Head().Tree()).
		SetContainer("ctr", dag.Container().From("golang")).
		WithPrompt("Mount $repo into $ctr and build ./cmd/booklit").
		WithPrompt("Disable CGo for maximum compatibility.").
		WithPrompt("Return the binary as a File.").
		File()
}
