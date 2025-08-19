//

package main

import (
	"dagger/claude/internal/dagger"
)

type Claude struct {
	Source  *dagger.Directory
	Sandbox *dagger.Container
}

func New(
	source *dagger.Directory,
	// +optional
	sandbox *dagger.Container,
) *Claude {
	if sandbox == nil {
		sandbox = dag.Wolfi().Container(dagger.WolfiContainerOpts{
			Packages: []string{"bash", "nodejs", "npm", "git"},
		})
	}
	return &Claude{
		Source:  source,
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
				AsService(),
		)
}
