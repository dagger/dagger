package main

import (
	"dagger/evals/internal/dagger"
	"fmt"
	"time"
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
		SetContainer("ctr",
			dag.Container().
				From("golang").
				WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
				WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
				WithMountedCache("/go/build-cache", dag.CacheVolume("go-build")).
				WithEnvVariable("GOCACHE", "/go/build-cache").
				WithEnvVariable("BUSTER", fmt.Sprintf("%d-%s", m.Attempt, time.Now())),
		).
		WithPrompt("Mount $repo into $ctr, set it as your workdir, and build ./cmd/booklit with CGO_ENABLED=0.").
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
