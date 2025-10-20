package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test that the model is conscious of a "current state" without needing
// explicit prompting.
func (m *Evals) CoreAPI() *CoreAPI {
	return &CoreAPI{}
}

type CoreAPI struct{}

func (e *CoreAPI) Name() string {
	return "CoreAPI"
}

func (e *CoreAPI) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env(dagger.EnvOpts{Privileged: true}).
			WithFileOutput("starch", "A file containing the word potato")).
		WithPrompt("Create a file that contains the word potato, and return it.").
		Loop()
}

func (e *CoreAPI) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		reply, err := prompt.Env().Output("starch").AsFile().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, reply, "potato")
	})
}
