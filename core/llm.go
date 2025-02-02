package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/dagger/dagger/core/bbi"
	_ "github.com/dagger/dagger/core/bbi/empty"
	_ "github.com/dagger/dagger/core/bbi/flat"
	"github.com/dagger/dagger/dagql"
	"github.com/joho/godotenv"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/codes"
)

// An instance of a LLM (large language model), with its state and tool calling environment
type Llm struct {
	Query *Query
	srv   *dagql.Server

	Config *LlmConfig

	// History of messages
	// FIXME: rename to 'messages'
	history []openai.ChatCompletionMessageParamUnion
	// History of tool calls and their result
	calls map[string]string

	// LLM state
	// Can hold typed variables for all the types available in the schema
	// This state is what gets extended by our graphql middleware
	// FIXME: Agent.ref moves here
	// FIXME: Agent.self moves here
	// FIXME: Agent.selfType moves here
	// FIXME: Agent.Self moves here
	// state map[string]dagql.Typed
	state dagql.Typed
}

type LlmConfig struct {
	Model string
	Key   string
	Host  string
	Path  string
}

// FIXME: engine-wide global config
// this is a workaround to enable modules to "just work" without bringing their own config
var globalLlmConfig *LlmConfig

func loadGlobalLlmConfig(ctx context.Context, srv *dagql.Server) (*LlmConfig, error) {
	loadSecret := func(uri string) (string, error) {
		var result string
		if u, err := url.Parse(uri); err != nil || u.Scheme == "" {
			result = uri
		} else if err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{
				Field: "secret",
				Args: []dagql.NamedInput{
					{
						Name:  "uri",
						Value: dagql.NewString(uri),
					},
				},
			},
			dagql.Selector{
				Field: "plaintext",
			},
		); err != nil {
			return "", err
		}
		return result, nil
	}
	if globalLlmConfig != nil {
		return globalLlmConfig, nil
	}
	cfg := new(LlmConfig)
	envFile, err := loadSecret("file://.env")
	if err != nil {
		return nil, err
	}
	env, err := godotenv.Unmarshal(string(envFile))
	if err != nil {
		return nil, err
	}
	// Configure API key
	if keyConfig, ok := env["LLM_KEY"]; ok {
		key, err := loadSecret(keyConfig)
		if err != nil {
			return nil, err
		}
		cfg.Key = key
	}
	if host, ok := env["LLM_HOST"]; ok {
		cfg.Host = host
	}
	if path, ok := env["LLM_PATH"]; ok {
		cfg.Path = path
	}
	if model, ok := env["LLM_MODEL"]; ok {
		cfg.Model = model
	} else {
		cfg.Model = "gpt-4o"
	}
	if cfg.Key == "" && cfg.Host == "" {
		return nil, fmt.Errorf("error loading llm configuration: .env must set LLM_KEY or LLM_HOST")
	}
	globalLlmConfig = cfg
	return cfg, nil
}

func NewLlm(ctx context.Context, query *Query, srv *dagql.Server) (*Llm, error) {
	// FIXME: make the llm key/host/path/model configurable
	config, err := loadGlobalLlmConfig(ctx, srv)
	if err != nil {
		return nil, err
	}
	return &Llm{
		Query:  query,
		srv:    srv,
		Config: config,
		calls:  make(map[string]string),
		// FIXME: support multiple variables in state
		//state:  make(map[string]dagql.Typed),
	}, nil
}

func (*Llm) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Llm",
		NonNull:   true,
	}
}

func (llm *Llm) Clone() *Llm {
	cp := *llm
	cp.history = cloneSlice(cp.history)
	cp.calls = cloneMap(cp.calls)
	// FIXME: support multiple variables in state
	// cp.state = cloneMap(cp.state)
	return &cp
}

// Generate a human-readable documentation of tools available to the model via BBI
func (llm *Llm) ToolsDoc(ctx context.Context) (string, error) {
	session, err := llm.BBI()
	if err != nil {
		return "", err
	}
	var result string
	for _, tool := range session.Tools() {
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
func (llm *Llm) Ask(ctx context.Context, question string) (string, error) {
	llm, err := llm.WithPrompt(ctx, question, false)
	if err != nil {
		return "", err
	}
	return llm.LastReply()
}

func (llm *Llm) Please(ctx context.Context, task string) (*Llm, error) {
	return llm.WithPrompt(ctx, task, false)
}

// Append a user message (prompt) to the message history
func (llm *Llm) WithPrompt(ctx context.Context, prompt string, lazy bool) (*Llm, error) {
	llm = llm.Clone()
	llm.history = append(llm.history, openai.UserMessage(prompt))
	if lazy {
		return llm, nil
	}
	return llm.Run(ctx, 0)
}

// Append a system prompt message to the history
func (llm *Llm) WithSystemPrompt(prompt string) *Llm {
	llm = llm.Clone()
	llm.history = append(llm.history, openai.SystemMessage(prompt))
	return llm
}

// Return the last message sent by the agent
func (llm *Llm) LastReply() (string, error) {
	messages, err := llm.messages()
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
func (llm *Llm) BBI() (bbi.Session, error) {
	var target dagql.Object
	// FIX<E: support multiple variables in state
	//	for _, val := range llm.state {
	//		obj, isObj := val.(dagql.Object)
	//		if isObj {
	//			target = obj
	//			break
	//		}
	//	}
	if llm.state != nil {
		target = llm.state.(dagql.Object)
	}
	return bbi.NewSession("flat", target, llm.srv)
}

func (llm *Llm) Run(
	ctx context.Context,
	maxLoops int,
) (*Llm, error) {
	llm = llm.Clone()
	// Start a new BBI session
	session, err := llm.BBI()
	if err != nil {
		return nil, err
	}
	for i := 0; maxLoops == 0 || i < maxLoops; i++ {
		tools := session.Tools()
		res, err := llm.sendQuery(ctx, tools)
		if err != nil {
			return nil, err
		}
		reply := res.Choices[0].Message
		// Add the model reply to the history
		if reply.Content != "" {
			_, span := Tracer(ctx).Start(ctx, "🤖💬 "+reply.Content)
			span.End()
		}
		llm.history = append(llm.history, reply)
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
						return llm, fmt.Errorf("failed to unmarshal arguments: %w", err)
					}
					result := func() string {
						ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("🤖 💻 %s(%s)", call.Function.Name, call.Function.Arguments))
						defer span.End()
						result, err := tool.Call(ctx, args)
						if err != nil {
							// If the BBI driver itself returned an error,
							// send that error to the model
							span.SetStatus(codes.Error, err.Error())
							return fmt.Sprintf("error calling tool: %s", err.Error())
						}
						switch v := result.(type) {
						case string:
							return v
						default:
							jsonBytes, err := json.Marshal(v)
							if err != nil {
								span.SetStatus(codes.Error, err.Error())
								return fmt.Sprintf("error processing tool result: %s", err.Error())
							}
							return string(jsonBytes)
						}
					}()
					func() {
						_, span := Tracer(ctx).Start(ctx, fmt.Sprintf("💻 %s", result))
						span.End()
						llm.calls[call.ID] = result
						llm.history = append(llm.history, openai.ToolMessage(call.ID, result))
					}()
				}
			}
		}
	}
	llm.state = session.Self()
	return llm, nil
}

func (llm *Llm) History() ([]string, error) {
	messages, err := llm.messages()
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
			if len(txt) > 0 {
				history = append(history, "🤖 💬"+txt)
			}
			for _, call := range msg.ToolCalls {
				history = append(history, fmt.Sprintf("🤖 💻 %s(%s)", call.Function.Name, call.Function.Arguments))
				if result, ok := llm.calls[call.ID]; ok {
					history = append(history, fmt.Sprintf("💻 %s", result))
				}
			}
		}
	}
	return history, nil
}

func (llm *Llm) messages() ([]openAIMessage, error) {
	// FIXME: ugly hack
	data, err := json.Marshal(llm.history)
	if err != nil {
		return nil, err
	}
	var messages []openAIMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (llm *Llm) Set(ctx context.Context, key string, objId dagql.IDType) (*Llm, error) {
	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("SET %s=%#v", key, objId))
	defer span.End()
	//typedef := llm.srv.Schema().Types[value.Type().Name()]
	//isID := value.Type().IsCompatible(new(dagql.ID[dagql.Typed]).Type())
	//if !isID {
	//	return nil, fmt.Errorf("type %s (%T) is not ID", value.Type().Name(), value)
	//}
	//if isID {
	obj, err := llm.srv.Load(ctx, objId.ID())
	if err != nil {
		return nil, err
	}
	//}
	llm = llm.Clone()
	// FIXME: support multiple variables
	// llm.state[key] = value
	llm.state = obj
	return llm, nil
}

func (llm *Llm) Get(ctx context.Context, key string) (dagql.Typed, error) {
	return llm.state, nil
	// FIXME: support multiple variables in state
	//if val, ok := llm.state[key]; ok {
	//	return val, nil
	//}
	//return nil, fmt.Errorf("no value at key %s", key)
}

func (llm *Llm) sendQuery(ctx context.Context, tools []bbi.Tool) (res *openai.ChatCompletion, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "[🤖] 💭")
	defer func() {
		if rerr != nil {
			span.SetStatus(codes.Error, rerr.Error())
		}
		span.End()
	}()
	params := openai.ChatCompletionNewParams{
		Seed:     openai.Int(0),
		Model:    openai.F(openai.ChatModel(llm.Config.Model)),
		Messages: openai.F(llm.history),
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
	return llm.client().Chat.Completions.New(ctx, params)
}

// Create a new openai client
func (llm *Llm) client() *openai.Client {
	var opts []option.RequestOption
	opts = append(opts, option.WithHeader("Content-Type", "application/json"))
	if llm.Config.Key != "" {
		opts = append(opts, option.WithAPIKey(llm.Config.Key))
	}
	if llm.Config.Host != "" || llm.Config.Path != "" {
		var base url.URL
		base.Scheme = "https"
		base.Host = llm.Config.Host
		base.Path = llm.Config.Path
		opts = append(opts, option.WithBaseURL(base.String()))
	}
	return openai.NewClient(opts...)
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
