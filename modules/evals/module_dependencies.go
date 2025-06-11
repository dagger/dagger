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
func (m *Evals) ModuleDependencies(ctx context.Context) (*ModuleDependencies, error) {
	err := dag.ModuleSource("github.com/dagger/dagger-test-modules/llm-dir-module-depender").AsModule().Serve(ctx, dagger.ModuleServeOpts{
		IncludeDependencies: true,
	})
	if err != nil {
		return nil, err
	}
	return &ModuleDependencies{}, nil
}

type ModuleDependencies struct{}

func (e *ModuleDependencies) Name() string {
	return "ModuleDependencies"
}

func (e *ModuleDependencies) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env(dagger.EnvOpts{Privileged: true}).
			WithStringOutput("methods", "The list of methods that you can see.")).
		WithPrompt("List all of the methods that you can see.")
}

func (e *ModuleDependencies) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		reply, err := prompt.Env().Output("methods").AsString(ctx)
		require.NoError(t, err)
		require.Contains(t, reply, "llmTestModule")
		require.Contains(t, reply, "llmDirModuleDepender")
	})
}
