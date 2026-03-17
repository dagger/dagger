package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/runt"
)

// Test MCP server usage by setting up Claude Code MCP server and making edits.
func (m *Evals) ModelContextProtocol() *ModelContextProtocol {
	return &ModelContextProtocol{}
}

type ModelContextProtocol struct{}

func (e *ModelContextProtocol) Name() string {
	return "ModelContextProtocol"
}

func (e *ModelContextProtocol) Prompt(base *dagger.LLM) *dagger.LLM {
	// Set up the LLM with Claude Code MCP server and a workspace
	return base.
		WithMCPServer("claude",
			dag.Container().From("node").
				WithExec([]string{"npm", "install", "-g", "@anthropic-ai/claude-code"}).
				WithDefaultArgs([]string{"claude", "mcp", "serve"}).
				WithWorkdir("/work").
				AsService(),
		).
		WithSystemPrompt("Your workspace is /work. You MUST include it at the beginning of path arguments to make them absolute paths.").
		WithEnv(dag.Env().
			WithWorkspace(dag.Directory().
				WithNewFile("README.md", "# Sample Project\nThis is a test project.\n").
				WithNewFile("main.go", "package main\n\nfunc main() {\n\tprintln(\"Hello, World!\")\n}\n")),
		).
		WithPrompt(`Please make the following changes to the workspace:

1. Update the README.md file to add a "Getting Started" section with installation instructions
2. Use Bash to rm main.go - we don't need it anymore.
3. Create a new file called "config.json" with some basic configuration settings

Make sure to use the file editing tools available through the MCP server to make these changes.`)
}

func (e *ModelContextProtocol) Check(ctx context.Context, prompt *dagger.LLM) error {
	return runt.Run(ctx, func(t testing.TB) {
		workspace := prompt.Env().Workspace()

		// Check that README.md was updated with Getting Started section
		readmeContent, err := workspace.File("README.md").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, readmeContent, "Getting Started", "README.md should contain a Getting Started section")

		entries, err := workspace.Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, entries, "main.go", "main.go should be removed")
		require.Contains(t, entries, "config.json", "config.json should be created")

		// Verify config.json has valid content
		configContent, err := workspace.File("config.json").Contents(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, configContent, "config.json should not be empty")
		require.Contains(t, configContent, "{", "config.json should contain JSON-like content")
	})
}
