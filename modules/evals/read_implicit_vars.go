package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test that the LLM is able to access the content of variables without the user
// having to expand them in the prompt.
func (m *Evals) ReadImplicitVars() *ReadImplicitVars {
	return &ReadImplicitVars{
		WeirdText: "I'm a strawberry!",
	}
}

type ReadImplicitVars struct {
	WeirdText string
}

func (e *ReadImplicitVars) Name() string {
	return "ReadImplicitVars"
}

func (e *ReadImplicitVars) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env().
			WithStringInput("myContent", e.WeirdText,
				"The content to write.").
			WithStringInput("desiredName", "/weird.txt",
				"The name of the file to write to.").
			WithDirectoryInput("dest", dag.Directory(),
				"The directory in which to write the file.").
			WithDirectoryOutput("out", "The directory containing the written file.")).
		WithPrompt("I gave you some content, a directory, and a filename. Write the content to the specified file in the directory.").
		Loop()
}

func (e *ReadImplicitVars) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		content, err := prompt.Env().
			Output("out").
			AsDirectory().
			File("weird.txt").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, e.WeirdText, content)
	})
}
