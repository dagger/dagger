package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/dagger/dagger/core/bbi"
	_ "github.com/dagger/dagger/core/bbi/flat"
	"github.com/dagger/dagger/dagql"
	"github.com/joho/godotenv"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/codes"
)

func NewLlmConfig() *LlmConfig {
	return &LlmConfig{
		Model: "gpt-4o", // default
	}
}

// Session-wide configuration for connecting to a LLM
// FIXME: move this to a client-side config instead, using session attachables
type LlmConfig struct {
	Model string
	Key   string
	Host  string
	Path  string
}

func (cfg LlmConfig) RequestOpts() (opts []option.RequestOption) {
	if cfg.Key != "" {
		opts = append(opts, option.WithAPIKey(cfg.Key))
	}
	if cfg.Host != "" || cfg.Path != "" {
		var base url.URL
		base.Scheme = "https"
		base.Host = cfg.Host
		base.Path = cfg.Path
		opts = append(opts, option.WithBaseURL(base.String()))
	}
	return opts
}

func NewAgent(srv *dagql.Server, self dagql.Object, selfType dagql.ObjectType) *Agent {
	a := &Agent{
		srv:      srv,
		self:     self,
		selfType: selfType,
	}
	if self == nil {
		// Gracefully support being a "zero value" for type introspection purposes
		// This means we will never actually be used. it is safe to return at this point
		return a
	}
	// Finish initializing if we have an actual instance
	a.def = srv.Schema().Types[selfType.TypeName()]
	a = a.WithSystemPrompt(fmt.Sprintf("You are a %s: %s\n\nWhen receiving and sending IDs, DO NOT change them. They are opaque identifiers.", a.def.Name, a.def.Description))
	return a
}

type Agent struct {
	history  []openai.ChatCompletionMessageParamUnion
	def      *ast.Definition
	srv      *dagql.Server
	self     dagql.Object
	selfType dagql.ObjectType
	count    int
}

func (a *Agent) Type() *ast.Type {
	return &ast.Type{
		NamedType: a.selfType.TypeName() + "Agent",
		NonNull:   true,
	}
}

func (a *Agent) Clone() *Agent {
	cp := *a
	cp.history = cloneSlice(cp.history)
	return &cp
}

func (a *Agent) Self(ctx context.Context) dagql.Object {
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("[🤖->📦] returning state %s", a.self.ID().Digest()))
	span.End()
	return a.self
}

// Generate a human-readable documentation of tools available to the model via BBI
func (a *Agent) ToolsDoc(ctx context.Context) (string, error) {
	var result string
	for _, tool := range a.BBI().Tools() {
		schema, err := json.MarshalIndent(tool.Schema, "", "  ")
		if err != nil {
			return "", err
		}
		result = fmt.Sprintf("%s## %s\n\n%s\n\n%s\n\n", result, tool.Name, tool.Description, string(schema))
	}
	return result, nil
}

// A convenience function to ask the model a question directly, and get an answer
// The state of the agent is not changed.
func (a *Agent) Ask(ctx context.Context, question string) (string, error) {
	a, err := a.WithPrompt(question).Run(ctx, 0)
	if err != nil {
		return "", err
	}
	return a.LastReply()
}

func (a *Agent) Do(ctx context.Context, task string) (*Agent, error) {
	return a.WithPrompt(task).Run(ctx, 0)
}

// Return the last message sent by the agent
func (a *Agent) LastReply() (string, error) {
	messages, err := a.messages()
	if err != nil {
		return "", err
	}
	var reply string = "(no reply)"
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		txt, err := msg.Text()
		if err != nil {
			return "", err
		}
		if len(txt) == 0 {
			continue
		}
		reply = txt
	}
	return reply, nil
}

// Start a new BBI (Brain-Body Interface) session.
// BBI allows a LLM to consume the Dagger API via tool calls
func (a *Agent) BBI() bbi.Session {
	// Hardcode the "one-one" BBI strategy
	return bbi.NewSession(a.self, a.srv)
}

func (a *Agent) Run(
	ctx context.Context,
	maxLoops int,
) (*Agent, error) {
	a = a.Clone()
	// Start a new BBI session
	session := a.BBI()
	for i := 0; maxLoops == 0 || i < maxLoops; i++ {
		tools := session.Tools()
		res, err := a.sendQuery(ctx, tools)
		if err != nil {
			return nil, err
		}
		reply := res.Choices[0].Message
		// Add the model reply to the history
		if reply.Content != "" {
			_, span := Tracer(ctx).Start(ctx, "🤖💬 "+reply.Content)
			span.End()
		}
		a.history = append(a.history, reply)
		// Handle tool calls
		calls := res.Choices[0].Message.ToolCalls
		if len(calls) == 0 {
			break
		}
		for _, call := range calls {
			for _, tool := range tools {
				if tool.Name == call.Function.Name {
					var args interface{}
					if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
						return a, fmt.Errorf("failed to unmarshal arguments: %w", err)
					}
					result, err := tool.Call(ctx, args)
					if err != nil {
						return nil, err
					}
					var resultStr string
					switch v := result.(type) {
					case string:
						resultStr = v
					default:
						jsonBytes, err := json.Marshal(v)
						if err != nil {
							return nil, err
						}
						resultStr = string(jsonBytes)
					}
					a.history = append(a.history, openai.ToolMessage(call.ID, resultStr))
				}
			}
		}
	}
	a.self = session.Self()
	return a, nil
}

func (a *Agent) mutate(ctx context.Context, sel dagql.Selector) error {
	val, id, err := a.self.Select(ctx, a.srv, sel)
	if err != nil {
		return err
	}
	self, err := a.self.ObjectType().New(id, val)
	if err != nil {
		return err
	}
	a.self = self
	return nil
}

func (a *Agent) History() ([]string, error) {
	messages, err := a.messages()
	if err != nil {
		return nil, err
	}
	var history []string
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			txt, err := msg.Text()
			if err != nil {
				return nil, err
			}
			history = append(history, "🧑 💬"+txt)
		case "assistant":
			txt, err := msg.Text()
			if err != nil {
				return nil, err
			}
			history = append(history, "🤖 💬"+txt)
			for _, call := range msg.ToolCalls {
				history = append(history, fmt.Sprintf("🤖 💻 %s(%s)", call.Function.Name, call.Function.Arguments))
			}
		}
	}
	return history, nil
}

// FIXME: engine-wide global config
// this is a workaround to enable modules to "just work" without bringing their own config
var globalLlmConfig *LlmConfig

func (a *Agent) llmConfig(ctx context.Context) (*LlmConfig, error) {
	if globalLlmConfig != nil {
		return globalLlmConfig, nil
	}
	// Load .env on client
	// Hack: share LLM config engine-wide
	var envFile dagql.Instance[*File]
	if err := a.srv.Select(ctx, a.srv.Root(), &envFile, dagql.Selector{
		Field: "host",
	}, dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(".env"),
			},
		},
	}); err != nil {
		return nil, err
	}
	contents, err := envFile.Self.Contents(ctx)
	if err != nil {
		return nil, err
	}
	env, err := godotenv.Unmarshal(string(contents))
	if err != nil {
		return nil, err
	}
	cfg := NewLlmConfig()
	// Configure API key
	if keyConfig, ok := env["LLM_KEY"]; ok {
		var key string
		if u, err := url.Parse(keyConfig); err != nil || u.Scheme == "" {
			key = keyConfig
		} else {
			if err := a.srv.Select(ctx, a.srv.Root(), &key,
				dagql.Selector{
					Field: "secret",
					Args: []dagql.NamedInput{
						{
							Name:  "uri",
							Value: dagql.NewString(keyConfig),
						},
					},
				},
				dagql.Selector{
					Field: "plaintext",
				},
			); err != nil {
				return nil, err
			}
			cfg.Key = key
		}
	}
	if host, ok := env["LLM_HOST"]; ok {
		cfg.Host = host
	}
	if path, ok := env["LLM_PATH"]; ok {
		cfg.Path = path
	}
	if model, ok := env["LLM_MODEL"]; ok {
		cfg.Model = model
	}
	if cfg.Key == "" && cfg.Host == "" {
		return nil, fmt.Errorf("error loading llm configuration: .env must set LLM_KEY or LLM_HOST")
	}
	globalLlmConfig = cfg
	return cfg, nil
}

func (a *Agent) sendQuery(ctx context.Context, tools []bbi.Tool) (res *openai.ChatCompletion, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "[🤖] 💭")
	defer func() {
		if rerr != nil {
			span.SetStatus(codes.Error, rerr.Error())
		}
		span.End()
	}()
	llmConfig, err := a.llmConfig(ctx)
	if err != nil {
		return nil, err
	}
	params := openai.ChatCompletionNewParams{
		Seed:     openai.Int(0),
		Model:    openai.F(openai.ChatModel(llmConfig.Model)),
		Messages: openai.F(a.history),
	}
	if len(tools) > 0 {
		var toolParams []openai.ChatCompletionToolParam
		for _, tool := range tools {
			toolParams = append(toolParams, openai.ChatCompletionToolParam{
				Type: openai.F(openai.ChatCompletionToolTypeFunction),
				Function: openai.F(openai.FunctionDefinitionParam{
					Name:        openai.String(tool.Name),
					Description: openai.String(tool.Description),
					Parameters:  openai.F(openai.FunctionParameters(tool.Schema)),
				}),
			})
		}
		params.Tools = openai.F(toolParams)
	}
	opts := append(
		llmConfig.RequestOpts(),
		option.WithHeader("Content-Type", "application/json"),
	)
	return openai.NewClient(opts...).Chat.Completions.New(ctx, params)
}

// Append a user message (prompt) to the message history
func (a *Agent) WithPrompt(prompt string) *Agent {
	a = a.Clone()
	a.history = append(a.history, openai.UserMessage(prompt))
	return a
}

// Append a system prompt message to the history
func (a *Agent) WithSystemPrompt(prompt string) *Agent {
	a = a.Clone()
	a.history = append(a.history, openai.SystemMessage(prompt))
	return a
}

func (s *Agent) messages() ([]openAIMessage, error) {
	// FIXME: ugly hack
	data, err := json.Marshal(s.history)
	if err != nil {
		return nil, err
	}
	var messages []openAIMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

type openAIMessage struct {
	Role       string      `json:"role", required`
	Content    interface{} `json:"content", required`
	ToolCallID string      `json:"tool_call_id"`
	ToolCalls  []struct {
		// The ID of the tool call.
		ID string `json:"id"`
		// The function that the model called.
		Function struct {
			Arguments string `json:"arguments"`
			// The name of the function to call.
			Name string `json:"name"`
		} `json:"function"`
		// The type of the tool. Currently, only `function` is supported.
		Type openai.ChatCompletionMessageToolCallType `json:"type"`
	} `json:"tool_calls"`
}

func (msg openAIMessage) Text() (string, error) {
	contentJson, err := json.Marshal(msg.Content)
	if err != nil {
		return "", err
	}
	switch msg.Role {
	case "user", "tool":
		var content []struct {
			Text string `json:"text", required`
		}
		if err := json.Unmarshal(contentJson, &content); err != nil {
			return "", fmt.Errorf("malformatted user or tool message: %s", err.Error())
		}
		if len(content) == 0 {
			return "", nil
		}
		return content[0].Text, nil
	case "assistant":
		var content string
		if err := json.Unmarshal(contentJson, &content); err != nil {
			return "", fmt.Errorf("malformatted assistant message: %#v", content)
		}
		return content, nil
	}
	return "", fmt.Errorf("unsupported message role: %s", msg.Role)
}

// Middleware called by the dagql server on each object installation
// For each object type <Foo>, we inject <Foo>Agent, a wrapper type that adds agent-like methods
// Essentially transforming every Dagger object into a LLM-powered robot
type AgentMiddleware struct {
	Server *dagql.Server
}

func (s AgentMiddleware) InstallObject(selfType dagql.ObjectType, install func(dagql.ObjectType)) {
	selfTypeName := selfType.TypeName()
	// Install the target type first, so we can reference it in our wrapper type
	if !s.isAgentMaterial(selfType) {
		install(selfType)
		return
	}
	agentType := dagql.NewClass[*Agent](dagql.ClassOpts[*Agent]{
		// Instantiate a throwaway agent instance from the type
		Typed: NewAgent(s.Server, nil, selfType),
	})
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "lastReply",
			Description: "return the agent's last reply, or an empty string",
			Type:        dagql.String(""),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*Agent]).Self
			reply, err := a.LastReply()
			if err != nil {
				return nil, err
			}
			return dagql.NewString(reply), nil
		},
		nil,
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "ask",
			Description: "ask the agent a question, without changing its state",
			Type:        dagql.String(""),
			Args: dagql.InputSpecs{
				{
					Name:        "question",
					Description: "the question to ask",
					Type:        dagql.String(""),
				},
			},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*Agent]).Self
			reply, err := a.Ask(ctx, args["question"].(dagql.String).String())
			if err != nil {
				return nil, err
			}
			return dagql.NewString(reply), nil
		},
		nil,
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "please",
			Description: "ask nicely the agent to accomplish a task, and return its new state",
			Type:        agentType.Typed(),
			Args: dagql.InputSpecs{
				{
					Name:        "task",
					Description: "a description of the task to perform",
					Type:        dagql.String(""),
				},
			},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			before := self.(dagql.Instance[*Agent]).Self
			after, err := before.Do(ctx, args["task"].(dagql.String).String())
			if err != nil {
				return nil, err
			}
			return after, nil
		},
		nil,
	)
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
			a := self.(dagql.Instance[*Agent]).Self
			return a.WithPrompt(args["prompt"].(dagql.String).String()), nil
		},
		nil,
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "tools",
			Description: "dump the tools available to the model",
			Type:        dagql.String(""),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*Agent]).Self
			tools, err := a.ToolsDoc(ctx)
			if err != nil {
				return nil, err
			}
			return dagql.NewString(tools), nil
		},
		nil,
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
		nil,
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "run",
			Description: "run the agent",
			Type:        agentType.Typed(),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*Agent]).Self
			return a.Run(ctx, 0)
		},
		nil,
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "history",
			Description: "return the agent history: user prompts, agent replies, and tool calls",
			Type:        dagql.ArrayInput[dagql.String]{},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*Agent]).Self
			history, err := a.History()
			if err != nil {
				return nil, err
			}
			return dagql.NewStringArray(history...), nil
		},
		nil,
	)
	agentType.Extend(
		dagql.FieldSpec{
			Name:        "state",
			Description: fmt.Sprintf("retrieve the agent's state"),
			Type:        selfType.Typed(),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			a := self.(dagql.Instance[*Agent]).Self
			return a.Self(ctx), nil
		},
		nil,
	)
	selfType.Extend(
		dagql.FieldSpec{
			Name:        "agent",
			Description: fmt.Sprintf("convert %s to an agent", selfTypeName),
			Type:        agentType.Typed(),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			return NewAgent(s.Server, self, self.ObjectType()), nil
		},
		nil,
	)
	// Install the wrapper type
	install(selfType)
	install(agentType)
}

func (s AgentMiddleware) ModuleWithObject(ctx context.Context, mod *Module, selfType *TypeDef) (*Module, error) {
	if !selfType.AsObject.Valid {
		return nil, fmt.Errorf("expected object type, got %s", selfType.Kind)
	}
	selfType = selfType.Clone()
	selfTypeName := selfType.AsObject.Value.Name
	agentType := NewObjectTypeDef(
		"Agent"+selfTypeName,
		"An agent for interacting with an "+selfTypeName,
	)

	selfTypeRef := &TypeDef{
		Kind: TypeDefKindObject,
		AsObject: dagql.Nullable[*ObjectTypeDef]{
			Value: &ObjectTypeDef{
				Name:         selfTypeName,
				OriginalName: selfTypeName,
			},
			Valid: true,
		},
	}

	agentTypeRef := &TypeDef{
		Kind: TypeDefKindObject,
		AsObject: dagql.Nullable[*ObjectTypeDef]{
			Value: &ObjectTypeDef{
				Name:         agentType.Name,
				OriginalName: agentType.Name,
			},
			Valid: true,
		},
	}

	agentType.Fields = append(agentType.Fields, &FieldTypeDef{
		Name:        "lastReply",
		Description: "return the agent's last reply, or an empty string",
		TypeDef: &TypeDef{
			Kind: TypeDefKindString,
		},
	})

	agentType.Functions = append(agentType.Functions, &Function{
		Name:        "ask",
		Description: "ask the agent a question, without changing its state",
		ReturnType: &TypeDef{
			Kind: TypeDefKindString,
		},
		Args: []*FunctionArg{
			{
				Name:        "question",
				Description: "the question to ask",
				TypeDef: &TypeDef{
					Kind: TypeDefKindString,
				},
			},
		},
	})

	agentType.Functions = append(agentType.Functions, &Function{
		Name:        "please",
		Description: "ask nicely the agent to accomplish a task, and return its new state",
		ReturnType:  agentTypeRef,
		Args: []*FunctionArg{
			{
				Name:        "task",
				Description: "a description of the task to perform",
				TypeDef: &TypeDef{
					Kind: TypeDefKindString,
				},
			},
		},
	})

	agentType.Functions = append(agentType.Functions, &Function{
		Name:        "withPrompt",
		Description: "add a prompt to the agent context",
		ReturnType:  agentTypeRef,
		Args: []*FunctionArg{
			{
				Name:        "prompt",
				Description: "the prompt",
				TypeDef: &TypeDef{
					Kind: TypeDefKindString,
				},
			},
		},
	})

	agentType.Fields = append(agentType.Fields, &FieldTypeDef{
		Name:        "tools",
		Description: "dump the tools available to the model",
		TypeDef: &TypeDef{
			Kind: TypeDefKindString,
		},
	})

	agentType.Fields = append(agentType.Fields, &FieldTypeDef{
		Name:        "model",
		Description: "return the model used by the agent",
		TypeDef: &TypeDef{
			Kind: TypeDefKindString,
		},
	})

	agentType.Fields = append(agentType.Fields, &FieldTypeDef{
		Name:        "run",
		Description: "run the agent",
		TypeDef:     agentTypeRef,
	})

	agentType.Fields = append(agentType.Fields, &FieldTypeDef{
		Name:        "history",
		Description: "return the agent history: user prompts, agent replies, and tool calls",
		TypeDef: (&TypeDef{}).WithListOf(&TypeDef{
			Kind: TypeDefKindString,
		}),
	})

	agentType.Fields = append(agentType.Fields, &FieldTypeDef{
		Name:        "state",
		Description: fmt.Sprintf("retrieve the agent's state"),
		TypeDef:     selfTypeRef,
	})

	// Add asAgent field to original type
	selfType.AsObject.Value.Fields = append(selfType.AsObject.Value.Fields, &FieldTypeDef{
		Name:        "agent",
		Description: fmt.Sprintf("convert the %s to an agent", selfTypeName),
		TypeDef:     agentTypeRef,
	})

	mod, err := mod.WithObject(ctx, selfType)
	if err != nil {
		return nil, err
	}

	return mod.WithObject(ctx, &TypeDef{
		Kind: TypeDefKindObject,
		AsObject: dagql.Nullable[*ObjectTypeDef]{
			Value: agentType,
			Valid: true,
		},
	})
}

// return true if a given object type should be upgraded with agent capabilities
func (s AgentMiddleware) isAgentMaterial(selfType dagql.ObjectType) bool {
	if strings.HasPrefix(selfType.TypeName(), "_") {
		return false
	}
	return true
}

var GlobalLLMConfig LlmConfig

func (s AgentMiddleware) llmConfig() LlmConfig {
	// The LLM config is attached to the root query object, as a global configuration.
	// We retrieve it via the low-level dagql server.
	// It can't be retrieved more directly, because the `asAgent` fields are attached
	// to all object types in the schema, and therefore we need a retrieval method
	// that doesn't require access to the parent's concrete type
	//
	// Note: only call this after the `Query` has been installed on the server

	// FIXME
	// return s.srv.Root().(dagql.Instance[*Query]).Self.LlmConfig
	return GlobalLLMConfig
}
