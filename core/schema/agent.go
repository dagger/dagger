package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type agentSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &agentSchema{}

func (s agentSchema) Install() {
	s.srv.SetMiddleware(core.AgentMiddleware{Server: s.srv})
}

func (s *agentSchema) withLlm(ctx context.Context, parent *core.Query, args core.LlmConfig) (*core.Query, error) {
	// FIXME: hack
	core.GlobalLLMConfig = args
	return parent.WithLlmConfig(args), nil
}

type agentWithPromptArgs struct {
	Prompt string
}

func (s *agentSchema) withPrompt(ctx context.Context, parent *core.Agent, args agentWithPromptArgs) (*core.Agent, error) {
	return parent.WithPrompt(args.Prompt), nil
}

func (s *agentSchema) withSystemPrompt(ctx context.Context, parent *core.Agent, args agentWithPromptArgs) (*core.Agent, error) {
	return parent.WithSystemPrompt(args.Prompt), nil
}

type agentRunArgs struct{}

func (s *agentSchema) run(ctx context.Context, parent *core.Agent, args agentRunArgs) (*core.Agent, error) {
	// FIXME: make maxLoops configurable
	return parent.Run(ctx, 0)
}

type agentHistoryArgs struct{}

func (s *agentSchema) history(ctx context.Context, parent *core.Agent, args agentHistoryArgs) ([]string, error) {
	return parent.History()
}

type agentAsObjectArgs struct{}

func (s *agentSchema) asObject(ctx context.Context, parent *core.Agent, args agentHistoryArgs) (dagql.Object, error) {
	return parent.Self(ctx), nil
}
