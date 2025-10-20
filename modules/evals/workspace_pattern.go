package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test the common workspace pattern.
func (m *Evals) WorkspacePattern() *WorkspacePattern {
	return &WorkspacePattern{}
}

type WorkspacePattern struct{}

func (e *WorkspacePattern) Name() string {
	return "WorkspacePattern"
}

func (e *WorkspacePattern) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env().
			WithWorkspaceInput("dir", dag.Workspace(time.Now().String()),
				"Your workspace for performing research.").
			WithWorkspaceOutput("out",
				"The workspace containing your facts."),
		).
		WithPrompt(`You are a researcher with convenient access to new facts. Research and record three facts. Don't rely on your own knowledge - only rely on the workspace. You can't find a new fact until you've recorded the last one.`).
		Loop()
}

func (e *WorkspacePattern) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		facts, err := prompt.Env().Output("out").AsWorkspace().Facts(ctx)
		require.NoError(t, err)
		model, err := prompt.Model(ctx)
		require.NoError(t, err)
		if slices.Contains(SmartModels, model) {
			require.ElementsMatch(t, []string{
				"The human body has at least five bones.",
				"Most sand is wet.",
				"Go is a programming language for garbage collection.",
			}, facts)
		} else {
			// can't expect much from local models atm
			require.NotEmpty(t, facts)
		}
	})
}
