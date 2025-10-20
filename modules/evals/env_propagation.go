package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test that @defaultPath propagates through nested module calls.
func (m *Evals) EnvPropagation() *EnvPropagation {
	return &EnvPropagation{}
}

type EnvPropagation struct{}

func (e *EnvPropagation) Name() string {
	return "EnvPropagation"
}

func (e *EnvPropagation) Prompt(ctx context.Context, base *dagger.LLM) (*dagger.LLM, error) {
	return base.
		WithEnv(dag.Env().
			WithModule(
				dag.CurrentModule().Source().
					Directory("./testdata/nested-context-middle").
					AsModule(),
			).
			WithWorkspace(
				dag.Directory().
					WithNewFile("marker.txt", "initial"),
			).
			WithStringOutput("marker", "The content read from the marker.")).
		WithPrompt("Update the marker with 'potato' and then read it.").
		Loop(), nil
}

func (e *EnvPropagation) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		env := prompt.Env()

		middleConfig, err := env.Output("marker").AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, "nested: POTATO!, middle: POTATO!", middleConfig)
	})
}
