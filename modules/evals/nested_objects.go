package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test that the LLM can navigate nested module objects that contain
// containers, without dumping huge objects in responses.
//
// This tests the fixes in commits:
// - 515031643b2e103156940498ecc1f94d5a0b7598 (skip object fields to avoid dumping huge objects)
// - 2420106614e8058bf7d3c045f3c42f742d9926f7 (expose tools for object field accessors)
func (m *Evals) NestedObjects() *NestedObjects {
	return &NestedObjects{}
}

type NestedObjects struct{}

func (e *NestedObjects) Name() string {
	return "NestedObjects"
}

func (e *NestedObjects) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env().
			WithModule(
				dag.CurrentModule().Source().
					Directory("./testdata/object-fields").
					AsModule(),
			).
			WithStringOutput("stdout", "The output of the container.")).
		WithPrompt("Call One, Two, and then Three, which will return you a container. Then, get me the container stdout. Stop early and respond with MAYDAY if you see a bunch of base64 encoded output in the response.")
}

func (e *NestedObjects) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		result, err := prompt.Env().Output("stdout").AsString(ctx)
		require.NoError(t, err)
		require.Contains(t, result, "potato")
	})
}
