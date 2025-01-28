package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

var llmConfig core.LlmConfig

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
	selfTypeName := selfType.TypeName()
	slog.Info("[agent middleware] installing original type: " + selfTypeName)
	// Install the target type first, so we can reference it in our wrapper type
	if !s.isAgentMaterial(selfType) {
		install(selfType)
		slog.Info("[agent middleware] not installing agent wrapper on special type " + selfTypeName)
		return
	}
	slog.Info("[agent middleware] installing wrapper type: " + selfTypeName + "Agent")
	defer slog.Info("[agent middleware] installed: " + selfTypeName + "Agent")
	agentType := dagql.NewClass[*core.Agent](dagql.ClassOpts[*core.Agent]{
		// Instantiate a throwaway agent instance from the type
		Typed: core.NewAgent(s.srv, core.LlmConfig{}, nil, selfType),
	})
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "withPrompt",
			Description: "add a prompt to the agent context",
			Type:        agentType.Typed(),
			Args: dagql.InputSpecs{
				{
					Name:        "prompt",
					Description: "the prompt",
					Type:        dagql.String(""),
				},
			},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*core.Agent]).Self
			return a.WithPrompt(args["prompt"].(dagql.String).String()), nil
		},
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "model",
			Description: "return the model used by the agent",
			Type:        dagql.String(""),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			return dagql.NewString(s.llmConfig().Model), nil
		},
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "run",
			Description: "run the agent",
			Type:        agentType.Typed(),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*core.Agent]).Self
			return a.Run(ctx, 0)
		},
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "history",
			Description: "return the agent history: user prompts, agent replies, and tool calls",
			Type:        dagql.ArrayInput[dagql.String]{},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*core.Agent]).Self
			history, err := a.History()
			if err != nil {
				return nil, err
			}
			return dagql.NewStringArray(history...), nil
		},
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "asObject",
			Description: fmt.Sprintf("convert the agent back to a %s", selfTypeName),
			Type:        selfType.Typed(),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*core.Agent]).Self
			return a.Self(ctx), nil
		},
	)
	selfType.Extend(
		dagql.FieldSpec{
			Name:        "asAgent",
			Description: fmt.Sprintf("convert the agent back to a %s", selfTypeName),
			Type:        agentType.Typed(),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			return core.NewAgent(s.srv, s.llmConfig(), self, self.ObjectType()), nil
		},
	)
	// Install the wrapper type
	install(selfType)
	install(agentType)
}

// return true if a given object type should be upgraded with agent capabilities
func (s agentSchema) isAgentMaterial(selfType dagql.ObjectType) bool {
	if strings.HasPrefix(selfType.TypeName(), "_") {
		return false
	}
	return true
}

func (s agentSchema) llmConfig() core.LlmConfig {
	// The LLM config is attached to the root query object, as a global configuration.
	// We retrieve it via the low-level dagql server.
	// It can't be retrieved more directly, because the `asAgent` fields are attached
	// to all object types in the schema, and therefore we need a retrieval method
	// that doesn't require access to the parent's concrete type
	//
	// Note: only call this after the `core.Query` has been installed on the server

	// FIXME
	// return s.srv.Root().(dagql.Instance[*core.Query]).Self.LlmConfig
	return llmConfig
}

func (s *agentSchema) withLlm(ctx context.Context, parent *core.Query, args core.LlmConfig) (*core.Query, error) {
	// FIXME: hack
	llmConfig = args
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
