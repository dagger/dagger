package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test various response types.
func (m *Evals) Writable() *Writable {
	return &Writable{}
}

type Writable struct{}

func (e *Writable) Name() string {
	return "Writable"
}

func (e *Writable) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env(dagger.EnvOpts{
			Writable:   true,
			Privileged: true,
		})).
		WithPrompt(`Create a new file, hello.txt, with the contents "Hello, world!".`).
		Loop().
		WithPrompt(`Save the File as a new output, "helloFile".`).
		Loop().
		WithPrompt(`Now declare another output "food" of type String, and save it as "potato".`)
}

func (e *Writable) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		contents, err := prompt.Env().Output("helloFile").AsFile().Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, world!", contents)
		strResult, err := prompt.Env().Output("food").AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, "potato", strResult)
	})
}
