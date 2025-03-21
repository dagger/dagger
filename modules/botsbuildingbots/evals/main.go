package main

import (
	"dagger/evals/internal/dagger"
)

type Evals struct {
	Model        string
	Attempt      int
	SystemPrompt string
}

func New(attempt int) *Evals {
	return &Evals{
		Attempt: attempt,
	}
}

func (m *Evals) WithModel(model string) *Evals {
	m.Model = model
	return m
}

func (m *Evals) WithSystemPrompt(prompt string) *Evals {
	m.SystemPrompt = prompt
	return m
}

func (m *Evals) UndoSingle() *dagger.Container {
	return m.LLM().
		WithQuery().
		WithPrompt("give me a container for PHP 7 development").
		Loop().
		WithPrompt("now install nano").
		Loop().
		WithPrompt("undo that and install vim instead").
		Loop().
		Container()
}

func (m *Evals) BuildMultiLLM() *dagger.LLM {
	return m.LLM().
		SetDirectory("repo", dag.Git("https://github.com/vito/booklit").Head().Tree()).
		SetContainer("ctr", dag.Container().From("golang")).
		WithPrompt("Mount $repo into $ctr and build ./cmd/booklit").
		WithPrompt("Disable CGo for maximum compatibility.").
		WithPrompt("Return the binary as a File.")
}

func (m *Evals) BuildMulti() *dagger.File {
	return m.BuildMultiLLM().File()
}

func (m *Evals) LLM() *dagger.LLM {
	llm := dag.LLM(dagger.LLMOpts{
		Model: m.Model,
	})
	if m.SystemPrompt != "" {
		llm = llm.WithSystemPrompt(m.SystemPrompt)
	}
	if m.Attempt > 0 {
		llm = llm.Attempt(m.Attempt)
	}
	return llm
}
