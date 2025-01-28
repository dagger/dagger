package schema

import (
	"context"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

type agentSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &agentSchema{}

func (s agentSchema) Install() {
	slog := slog.With("schema", "agent")
	// extend type Query { withLlm(): Query! }
	dagql.Fields[*core.Query]{
		dagql.Func("withLlm", s.withLlm).
			Doc(`Enable LLM integration`).
			ArgDoc("model", "The model to use").
			ArgDoc("key", "The API key for the LLM endpoint"),
	}.Install(s.srv)
	// Install ourselves as middleware
	slog.Info("[AGENT] installing middleware")
	s.srv.SetMiddleware(s)
}

// Middleware called by the dagql server on each object installation
// For each object type <Foo>, we inject <Foo>Agent, a wrapper type that adds agent-like methods
// Essentially transforming every Dagger object into a LLM-powered robot
func (s agentSchema) InstallObject(selfType dagql.ObjectType, install func(dagql.ObjectType)) {
	slog.Info("[AGENT] middleware installObject(%s)", selfType.Typed().Type().Name())
	// Install the target type first, so we can reference it in our wrapper type
	install(selfType)
	// Create the wrapper type
	class := dagql.NewClass[*core.Agent](dagql.ClassOpts[*core.Agent]{
		// Instantiate a throwaway agent instance from the type
		Typed: core.NewAgent(s.srv, core.LlmConfig{}, nil, selfType),
	})
	class.Install(
		dagql.Func("withPrompt", s.withPrompt).
			Doc("add a prompt to the agent context").
			ArgDoc("prompt", "The prompt. Example: \"make me a sandwich\""),
		dagql.Func("run", s.run).
			Doc("run the agent"),
		dagql.Func("history", s.history).
			Doc("return the agent history: user prompts, agent replies, and tool calls"),
		dagql.Func("as"+selfType.TypeName(), s.asObject).
			Doc("convert the agent back to a "+selfType.TypeName()),
	)
	// Install the wrapper type
	install(class)
}

func (s agentSchema) llmConfig() core.LlmConfig {
	// The LLM config is attached to the root query object, as a global configuration.
	// We retrieve it via the low-level dagql server.
	// It can't be retrieved more directly, because the `asAgent` fields are attached
	// to all object types in the schema, and therefore we need a retrieval method
	// that doesn't require access to the parent's concrete type
	//
	// Note: only call this after the `core.Query` has been installed on the server
	return s.srv.Root().(dagql.Instance[*core.Query]).Self.LlmConfig
}

func (s *agentSchema) withLlm(ctx context.Context, parent *core.Query, args core.LlmConfig) (*core.Query, error) {
	return parent.WithLlmConfig(args), nil
}

type asAgentArgs struct{}

func (s *agentSchema) asAgent(ctx context.Context, parent dagql.Object, args asAgentArgs) (*core.Agent, error) {
	return core.NewAgent(s.srv, s.llmConfig(), parent, parent.ObjectType()), nil
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
	return parent.Self(), nil
}
