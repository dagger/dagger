package main

import (
	"dagger/evals/internal/dagger"
)

// Test that BuildMulti works when configured with a static tool calling scheme.
func (m *Evals) BuildMultiStatic() *BuildMultiStatic {
	return &BuildMultiStatic{
		BuildMulti: m.BuildMulti(),
	}
}

type BuildMultiStatic struct {
	*BuildMulti
}

func (e *BuildMultiStatic) Name() string {
	return "BuildMultiStatic"
}

func (e *BuildMultiStatic) Prompt(base *dagger.LLM) *dagger.LLM {
	return e.BuildMulti.Prompt(base).WithStaticTools()
}
