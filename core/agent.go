package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/codes"
)

// Session-wide configuration for connecting to a LLM
// FIXME: move this to a client-side config instead, using session attachables
type LlmConfig struct {
	Model    string
	Key      SecretID
	Endpoint dagql.Optional[dagql.String]
}

func (*LlmConfig) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LlmConfig",
		NonNull:   true,
	}
}

func (*LlmConfig) TypeDescription() string {
	return "Configuration for integrating a LLM with the Dagger Engine"
}

// Retrieve the key plaintext
func (cfg *LlmConfig) KeyPlaintext(ctx context.Context, srv *dagql.Server) (string, error) {
	secrets, err := srv.Root().(dagql.Instance[*Query]).Self.Secrets(ctx)
	if err != nil {
		return "", err
	}
	b, ok := secrets.GetSecretPlaintext(cfg.Key.ID().Digest())
	if !ok {
		return "", fmt.Errorf("llm config: get key: secret look up failed")
	}
	return string(b), nil
}

func NewAgent(srv *dagql.Server, llmConfig LlmConfig, self dagql.Object, selfType dagql.ObjectType) *Agent {
	a := Agent{
		srv:       srv,
		self:      self,
		selfType:  selfType,
		def:       srv.Schema().Types[selfType.TypeName()],
		llmConfig: llmConfig,
	}
	return a.WithSystemPrompt(fmt.Sprintf("You are a %s: %s", a.def.Name, a.def.Description))
}

type Agent struct {
	history   []openai.ChatCompletionMessageParamUnion
	def       *ast.Definition
	srv       *dagql.Server
	self      dagql.Object
	selfType  dagql.ObjectType
	count     int
	llmConfig LlmConfig
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

func (a *Agent) Self() dagql.Object {
	return a.self
}

func (a *Agent) Run(
	ctx context.Context,
	maxLoops int,
) (*Agent, error) {
	a = a.Clone()
	// Hardcode the "one-one" BBI strategy
	bbi, err := OneOneBBI{}.NewSession(a.self, a.srv)
	if err != nil {
		return nil, err
	}
	for i := 0; maxLoops == 0 || i < maxLoops; i++ {
		tools := bbi.Tools()
		res, err := a.sendQuery(ctx, tools)
		if err != nil {
			return nil, err
		}
		reply := res.Choices[0].Message
		// Add the model reply to the history
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

func (a *Agent) sendQuery(ctx context.Context, tools []Tool) (res *openai.ChatCompletion, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "[🤖] 💭")
	defer func() {
		if rerr != nil {
			span.SetStatus(codes.Error, rerr.Error())
		}
		span.End()
	}()
	params := openai.ChatCompletionNewParams{
		Seed:     openai.Int(0),
		Model:    openai.F(openai.ChatModel(a.llmConfig.Model)),
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
	opts := []option.RequestOption{option.WithHeader("Content-Type", "application/json")}
	key, err := a.llmConfig.KeyPlaintext(ctx, a.srv)
	if err != nil {
		return nil, err
	}
	if key != "" {
		opts = append(opts, option.WithAPIKey(key))
	}
	if a.llmConfig.Endpoint.Valid {
		opts = append(opts, option.WithBaseURL(a.llmConfig.Endpoint.Value.String()))
	}
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
