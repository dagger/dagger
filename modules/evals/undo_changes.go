package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test the model's eagerness to switch to prior states instead of mutating the
// current state to undo past actions.
func (m *Evals) UndoChanges() *UndoChanges {
	return &UndoChanges{}
}

type UndoChanges struct{}

func (e *UndoChanges) Name() string {
	return "UndoChanges"
}

func (e *UndoChanges) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env().
			WithDirectoryInput("dir", dag.Directory(),
				"A directory in which to write files.").
			WithDirectoryOutput("out", "The directory with the desired contents.")).
		WithPrompt("Create the file /a with contents 1.").
		Loop().
		WithPrompt("Create the file /b with contents 2.").
		Loop().
		WithPrompt("Nevermind - go back to just /a and create /c with contents 3, and return that.").
		Loop()
}

func (e *UndoChanges) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		entries, err := prompt.Env().Output("out").AsDirectory().Entries(ctx)
		require.NoError(t, err)
		sort.Strings(entries)
		require.ElementsMatch(t, []string{"a", "c"}, entries)
	})
}
