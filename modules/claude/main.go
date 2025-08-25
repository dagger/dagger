//

package main

import (
	"dagger/claude/internal/dagger"
)

type Claude struct {
	Sandbox *dagger.Container
}

func New(
	// +optional
	sandbox *dagger.Container,
) *Claude {
	if sandbox == nil {
		sandbox = dag.Wolfi().Container(dagger.WolfiContainerOpts{
			Packages: []string{"bash", "nodejs", "npm", "git"},
		})
	}
	return &Claude{
		Sandbox: sandbox,
	}
}

// Returns an LLM with Claude Code installed as an MCP server.
func (m *Claude) Agent(
	// +optional
	base *dagger.LLM,
) *dagger.LLM {
	if base == nil {
		base = dag.LLM()
	}
	return base.
		WithMCPServer("claude",
			m.Sandbox.
				WithExec([]string{"npm", "install", "-g", "@anthropic-ai/claude-code"}).
				WithDefaultArgs([]string{"claude", "mcp", "serve"}).
				WithWorkdir("/work").
				AsService(),
		).
		WithSystemPrompt("Your current working directory is /work.")
}

// An entrypoint for starting Claude from the CLI with an arbitrary source
// directory.
func (m *Claude) Dev(source *dagger.Directory) *dagger.LLM {
	return m.Agent(
		dag.LLM().
			WithEnv(dag.Env().WithHostfs(source)),
	)
}
