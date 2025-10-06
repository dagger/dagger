package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test various response types.
func (m *Evals) Responses() *Responses {
	return &Responses{}
}

type Responses struct{}

func (e *Responses) Name() string {
	return "Responses"
}

func (e *Responses) Prompt(base *dagger.LLM) *dagger.LLM {
	return base.
		WithEnv(dag.Env().
			WithFileInput("hello_file",
				dag.File("foo.txt", "Hello, world!"),
				"The file to inspect.").
			WithModuleSourceInput("my_module",
				dag.ModuleSource("github.com/dagger/dagger-test-modules/llm-dir-module-depender"),
				"The module source to inspect.").
			WithDirectoryInput("some_dir",
				dag.Directory().
					WithNewFile("file-1", "").
					WithNewFile("file-2", ""),
				"The directory to inspect.").
			WithStringOutput("module_config_exists",
				"Whether the module config exists for the my_module input (true/false).").
			WithStringOutput("file_contents",
				"The contents of the file.").
			WithStringOutput("file_size",
				"The size of the file.").
			WithStringOutput("dir_entries",
				"Line-separated list of directory entries.")).
		WithPrompt("Provide the requested values.")
}

func (e *Responses) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		boolResult, err := prompt.Env().Output("module_config_exists").AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, "true", boolResult)
		strResult, err := prompt.Env().Output("file_contents").AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello, world!", strResult)
		intResult, err := prompt.Env().Output("file_size").AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, strconv.Itoa(len("Hello, world!")), intResult)
		listResult, err := prompt.Env().Output("dir_entries").AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, "file-1\nfile-2", listResult)
	})
}
