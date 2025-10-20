package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test manual intervention allowing the prompt to succeed.
func (m *Evals) LifeAlert() *LifeAlert {
	return &LifeAlert{}
}

type LifeAlert struct{}

func (e *LifeAlert) Name() string {
	return "LifeAlert"
}

func (e *LifeAlert) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env().
			WithDirectoryInput("dir", dag.Directory(), "A directory to write a file into.").
			WithFileOutput("file", "A file containing knowledge you don't have."),
		).
		WithPrompt("Ask me what to write to the file.").
		Loop()
}

func (e *LifeAlert) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		reply, err := prompt.Env().Output("file").AsFile().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.ToLower(reply), "potato")
	})
}
