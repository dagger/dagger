package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test basic prompting.
func (m *Evals) Basic() *Basic {
	return &Basic{}
}

type Basic struct{}

func (e *Basic) Name() string {
	return "Basic"
}

func (e *Basic) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithPrompt("Hello there! Simply respond with 'potato' and take no other action.").
		Loop()
}

func (e *Basic) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		reply, err := prompt.LastReply(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.ToLower(reply), "potato")
	})
}
