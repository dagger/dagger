package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"

	"dagger/evals/internal/dagger"
)

// Test the Doug coding agent.
func (m *Evals) AndOperator() *AndOperator {
	return &AndOperator{}
}

type AndOperator struct{}

func (e *AndOperator) Name() string {
	return "AndOperator"
}

func (e *AndOperator) Source() *dagger.Directory {
	return dag.Git("https://github.com/vito/dang").
		Commit("bcd9affe6f9d2d266d88ceae12a94b17d29e0917").
		Tree()
}

func (e *AndOperator) Prompt(base *dagger.LLM) *dagger.LLM {
	source := e.Source()
	env := base.Env().
		WithModule(source.AsModule()).
		WithWorkspace(source).
		WithFileOutput("dang", "The compiled Dang binary with && support")
	return dag.Doug().
		Agent(base.WithEnv(env)).
		WithPrompt("Implement support for the && operator. Don't bother with short-circuiting; keep it simple for now.")
}

func (e *AndOperator) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		implemented, err := prompt.Env().Output("dang").AsFile().Sync(ctx)
		require.NoError(t, err)

		checked, err := dag.Container().
			From("alpine").
			WithNewFile("/src/test.dang", "true && false").
			WithFile("/usr/bin/dang", implemented).
			WithExec([]string{"dang", "/src/test.dang"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ok!\n", checked)
	})
}
