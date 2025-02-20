package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/dagger/dagger/core/bbi"
	_ "github.com/dagger/dagger/core/bbi/empty"
	_ "github.com/dagger/dagger/core/bbi/flat"
	"github.com/dagger/dagger/dagql"
	"github.com/joho/godotenv"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// An instance of a LLM (large language model), with its state and tool calling environment
type Llm struct {
	Query *Query

	maxApiCalls int
	apiCalls    int
	Model       string
	Endpoint    *LlmEndpoint

	// If true: has un-synced state
	dirty bool
	// History of messages
	// FIXME: rename to 'messages'
	history []ModelMessage
	// History of tool calls and their result
	calls      map[string]string
	promptVars []string

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

type LlmEndpoint struct {
	Model    string
	BaseURL  string
	Key      string
	Provider LlmProvider
	Client   LLMClient
}

type LlmProvider string

// LLMClient interface defines the methods that each provider must implement
type LLMClient interface {
	SendQuery(ctx context.Context, history []ModelMessage, tools []bbi.Tool) (*LLMResponse, error)
}

type LLMResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// ModelMessage represents a generic message in the LLM conversation
type ModelMessage struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Function FuncCall `json:"function"`
	Type     string   `json:"type"`
}

type FuncCall struct {
	Arguments string `json:"arguments"`
	Name      string `json:"name"`
}

const (
	OpenAI    LlmProvider = "openai"
	Anthropic LlmProvider = "anthropic"
	Google    LlmProvider = "google"
	Meta      LlmProvider = "meta"
	Mistral   LlmProvider = "mistral"
	DeepSeek  LlmProvider = "deepseek"
	Other     LlmProvider = "other"
)

// A LLM routing configuration
type LlmRouter struct {
	ANTHROPIC_API_KEY  string
	ANTHROPIC_BASE_URL string
	ANTHROPIC_MODEL    string
	OPENAI_API_KEY     string
	OPENAI_BASE_URL    string
	OPENAI_MODEL       string
}

func (r *LlmRouter) isAnthropicModel(model string) bool {
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "anthropic/")
}

func (r *LlmRouter) isOpenAIModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "openai/")
}

func (r *LlmRouter) isGoogleModel(model string) bool {
	return strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "google/")
}

func (r *LlmRouter) isMistralModel(model string) bool {
	return strings.HasPrefix(model, "mistral-") || strings.HasPrefix(model, "mistral/")
}

func (r *LlmRouter) isLlamaModel(model string) bool {
	return strings.HasPrefix(model, "llama-") || strings.HasPrefix(model, "meta/")
}

func (r *LlmRouter) routeAnthropicModel() *LlmEndpoint {
	defaultSystemPrompt := "You are a helpful AI assistant. You can use tools to accomplish the user's requests"
	endpoint := &LlmEndpoint{
		BaseURL:  r.ANTHROPIC_BASE_URL,
		Key:      r.ANTHROPIC_API_KEY,
		Provider: Anthropic,
	}
	endpoint.Client = newAnthropicClient(endpoint, defaultSystemPrompt)

	return endpoint
}

func (r *LlmRouter) routeOpenAIModel() *LlmEndpoint {
	endpoint := &LlmEndpoint{
		BaseURL:  r.OPENAI_BASE_URL,
		Key:      r.OPENAI_API_KEY,
		Provider: OpenAI,
	}
	endpoint.Client = newOpenAIClient(endpoint)

	return endpoint
}

func (r *LlmRouter) routeOtherModel() *LlmEndpoint {
	// default to openAI compat from other providers
	endpoint := &LlmEndpoint{
		BaseURL:  r.OPENAI_BASE_URL,
		Key:      r.OPENAI_API_KEY,
		Provider: Other,
	}
	endpoint.Client = newOpenAIClient(endpoint)

	return endpoint
}

// Return a default model, if configured
func (r *LlmRouter) DefaultModel() string {
	for _, model := range []string{r.OPENAI_MODEL, r.ANTHROPIC_MODEL} {
		if model != "" {
			return model
		}
	}
	if r.OPENAI_API_KEY != "" {
		return "gpt-4o"
	}
	if r.ANTHROPIC_API_KEY != "" {
		return anthropic.ModelClaude3_5SonnetLatest
	}
	if r.OPENAI_BASE_URL != "" {
		return "llama-3.2"
	}
	return ""
}

// Return an endpoint for the requested model
// If the model name is not set, a default will be selected.
func (r *LlmRouter) Route(model string) (*LlmEndpoint, error) {
	if model == "" {
		model = r.DefaultModel()
	}
	var endpoint *LlmEndpoint
	if r.isAnthropicModel(model) {
		endpoint = r.routeAnthropicModel()
	} else if r.isOpenAIModel(model) {
		endpoint = r.routeOpenAIModel()
	} else if r.isGoogleModel(model) {
		return nil, fmt.Errorf("Google models are not yet supported")
	} else if r.isMistralModel(model) {
		return nil, fmt.Errorf("Mistral models are not yet supported")
	} else {
		endpoint = r.routeOtherModel()
	}
	endpoint.Model = model
	return endpoint, nil
}

func (cfg *LlmRouter) LoadConfig(ctx context.Context, getenv func(context.Context, string) (string, error)) error {
	if getenv == nil {
		getenv = func(ctx context.Context, key string) (string, error) {
			return os.Getenv(key), nil
		}
	}
	var err error
	cfg.ANTHROPIC_API_KEY, err = getenv(ctx, "ANTHROPIC_API_KEY")
	if err != nil {
		return err
	}
	cfg.ANTHROPIC_BASE_URL, err = getenv(ctx, "ANTHROPIC_BASE_URL")
	if err != nil {
		return err
	}
	cfg.ANTHROPIC_MODEL, err = getenv(ctx, "ANTHROPIC_MODEL")
	if err != nil {
		return err
	}
	cfg.OPENAI_API_KEY, err = getenv(ctx, "OPENAI_API_KEY")
	if err != nil {
		return err
	}
	cfg.OPENAI_BASE_URL, err = getenv(ctx, "OPENAI_BASE_URL")
	if err != nil {
		return err
	}
	cfg.OPENAI_MODEL, err = getenv(ctx, "OPENAI_MODEL")
	if err != nil {
		return err
	}
	return nil
}

func NewLlmRouter(ctx context.Context, srv *dagql.Server) (*LlmRouter, error) {
	router := new(LlmRouter)
	// Get the secret plaintext, from either a URI (provider lookup) or a plaintext (no-op)
	loadSecret := func(ctx context.Context, uriOrPlaintext string) (string, error) {
		var result string
		if u, err := url.Parse(uriOrPlaintext); err == nil && (u.Scheme == "op" || u.Scheme == "vault" || u.Scheme == "env" || u.Scheme == "file") {
			// If it's a valid secret reference:
			if err := srv.Select(ctx, srv.Root(), &result,
				dagql.Selector{
					Field: "secret",
					Args:  []dagql.NamedInput{{Name: "uri", Value: dagql.NewString(uriOrPlaintext)}},
				},
				dagql.Selector{
					Field: "plaintext",
				},
			); err != nil {
				return "", err
			}
			return result, nil
		}
		// If it's a regular plaintext:
		return uriOrPlaintext, nil
	}
	env := make(map[string]string)
	// Load .env from current directory, if it exists
	if envFile, err := loadSecret(ctx, "file://.env"); err == nil {
		if e, err := godotenv.Unmarshal(string(envFile)); err == nil {
			env = e
		}
	}
	err := router.LoadConfig(ctx, func(ctx context.Context, k string) (string, error) {
		// First lookup in the .env file
		if v, ok := env[k]; ok {
			return loadSecret(ctx, v)
		}
		// Second: lookup in client env directly
		if v, err := loadSecret(ctx, "env://"+k); err == nil {
			// Allow the env var itself to be a secret reference
			return loadSecret(ctx, v)
		}
		return "", nil
	})
	return router, err
}

func NewLlm(ctx context.Context, query *Query, srv *dagql.Server, model string, maxApiCalls int) (*Llm, error) {
	var router *LlmRouter
	{
		// Don't leak this context, it's specific to querying the parent client for llm config secrets
		// FIXME: clean up this function
		ctx, mainSrv, err := query.MainServer(ctx)
		if err != nil {
			return nil, err
		}
		router, err = NewLlmRouter(ctx, mainSrv)
		if err != nil {
			return nil, err
		}
	}
	if model == "" {
		model = router.DefaultModel()
	}
	endpoint, err := router.Route(model)
	if err != nil {
		return nil, err
	}
	if endpoint.Model == "" {
		return nil, fmt.Errorf("No valid LLM endpoint configuration")
	}
	// FIXME: merge model into endpoint
	return &Llm{
		Query:       query,
		Model:       model,
		Endpoint:    endpoint,
		maxApiCalls: maxApiCalls,
		calls:       make(map[string]string),
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
	cp.promptVars = cloneSlice(cp.promptVars)
	cp.calls = cloneMap(cp.calls)
	// FIXME: support multiple variables in state
	// cp.state = cloneMap(cp.state)
	return &cp
}

// Generate a human-readable documentation of tools available to the model via BBI
func (llm *Llm) ToolsDoc(ctx context.Context, srv *dagql.Server) (string, error) {
	session, err := llm.BBI(srv)
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

// Append a user message (prompt) to the message history
func (llm *Llm) WithPrompt(
	ctx context.Context,
	// The prompt message.
	prompt string,
	srv *dagql.Server,
) (*Llm, error) {
	vars := llm.promptVars
	if len(vars) > 0 {
		prompt = os.Expand(prompt, func(key string) string {
			// Iterate through vars array taking elements in pairs, looking
			// for a key that matches the template variable being expanded
			for i := 0; i < len(vars)-1; i += 2 {
				if vars[i] == key {
					return vars[i+1]
				}
			}
			// If vars array has odd length and the last key has no value,
			// return empty string when that key is looked up
			if len(vars)%2 == 1 && vars[len(vars)-1] == key {
				return ""
			}
			return key
		})
	}
	llm = llm.Clone()
	func() {
		ctx, span := Tracer(ctx).Start(ctx, "LLM prompt", telemetry.Reveal(), trace.WithAttributes(
			attribute.String(telemetry.UIActorEmojiAttr, "🧑"),
			attribute.String(telemetry.UIMessageAttr, "sent"),
		))
		defer span.End()
		stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
			log.String(telemetry.ContentTypeAttr, "text/markdown"))
		defer stdio.Close()
		fmt.Fprint(stdio.Stdout, prompt)
	}()
	llm.history = append(llm.history, ModelMessage{
		Role:    "user",
		Content: prompt,
	})
	llm.dirty = true
	return llm, nil
}

// WithPromptFile is like WithPrompt but reads the prompt from a file
func (llm *Llm) WithPromptFile(ctx context.Context, file *File, srv *dagql.Server) (*Llm, error) {
	contents, err := file.Contents(ctx)
	if err != nil {
		return nil, err
	}
	return llm.WithPrompt(ctx, string(contents), srv)
}

func (llm *Llm) WithPromptVar(name, value string) *Llm {
	llm = llm.Clone()
	llm.promptVars = append(llm.promptVars, name, value)
	return llm
}

// Append a system prompt message to the history
func (llm *Llm) WithSystemPrompt(prompt string) *Llm {
	llm = llm.Clone()
	llm.history = append(llm.history, ModelMessage{
		Role:    "system",
		Content: prompt,
	})
	llm.dirty = true
	return llm
}

// Return the last message sent by the agent
func (llm *Llm) LastReply(ctx context.Context, dag *dagql.Server) (string, error) {
	llm, err := llm.Sync(ctx, dag)
	if err != nil {
		return "", err
	}
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
func (llm *Llm) BBI(srv *dagql.Server) (bbi.Session, error) {
	var target dagql.Object
	if llm.state != nil {
		target = llm.state.(dagql.Object)
	}
	return bbi.NewSession("flat", target, srv)
}

// send the context to the LLM endpoint, process replies and tool calls; continue in a loop
// Synchronize LLM state:
// 1. Send context to LLM endpoint
// 2. Process replies and tool calls
// 3. Continue in a loop until no tool calls, or caps are reached
func (llm *Llm) Sync(ctx context.Context, dag *dagql.Server) (*Llm, error) {
	if !llm.dirty {
		return llm, nil
	}
	llm = llm.Clone()
	// Start a new BBI session
	session, err := llm.BBI(dag)
	if err != nil {
		return nil, err
	}
	for {
		if llm.maxApiCalls > 0 && llm.apiCalls >= llm.maxApiCalls {
			return nil, fmt.Errorf("reached API call limit: %d", llm.apiCalls)
		}
		llm.apiCalls++

		tools := session.Tools()
		res, err := llm.Endpoint.Client.SendQuery(ctx, llm.history, tools)
		if err != nil {
			return nil, err
		}

		// Add the model reply to the history
		llm.history = append(llm.history, ModelMessage{
			Role:      "assistant",
			Content:   res.Content,
			ToolCalls: res.ToolCalls,
		})
		// Handle tool calls
		// calls := res.Choices[0].Message.ToolCalls
		if len(res.ToolCalls) == 0 {
			break
		}
		for _, toolCall := range res.ToolCalls {
			for _, tool := range tools {
				if tool.Name == toolCall.Function.Name {
					var args map[string]any
					decoder := json.NewDecoder(strings.NewReader(toolCall.Function.Arguments))
					decoder.UseNumber()
					if err := decoder.Decode(&args); err != nil {
						return llm, fmt.Errorf("failed to unmarshal arguments: %w", err)
					}
					result := func() string {
						ctx, span := Tracer(ctx).Start(ctx,
							fmt.Sprintf("🤖 💻 %s", toolCall.Function.Name),
							telemetry.Passthrough(),
							telemetry.Reveal())
						defer span.End()
						result, err := tool.Call(ctx, args)
						if err != nil {
							// If the BBI driver itself returned an error,
							// send that error to the model
							span.SetStatus(codes.Error, err.Error())
							return fmt.Sprintf("error calling tool %q: %s", tool.Name, err.Error())
						}
						stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
						defer stdio.Close()
						switch v := result.(type) {
						case string:
							fmt.Fprint(stdio.Stdout, v)
							return v
						default:
							jsonBytes, err := json.Marshal(v)
							if err != nil {
								span.SetStatus(codes.Error, err.Error())
								return fmt.Sprintf("error processing tool result: %s", err.Error())
							}
							fmt.Fprint(stdio.Stdout, string(jsonBytes))
							return string(jsonBytes)
						}
					}()
					func() {
						llm.calls[toolCall.ID] = result
						llm.history = append(llm.history, ModelMessage{
							Role:    "assistant",
							Content: result,
						})
					}()
				}
			}
		}
	}
	llm.state = session.Self()
	llm.dirty = false
	return llm, nil
}

func (llm *Llm) History(ctx context.Context, dag *dagql.Server) ([]string, error) {
	llm, err := llm.Sync(ctx, dag)
	if err != nil {
		return nil, err
	}
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

func (llm *Llm) messages() ([]ModelMessage, error) {
	// FIXME: ugly hack
	data, err := json.Marshal(llm.history)
	if err != nil {
		return nil, err
	}
	var messages []ModelMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (llm *Llm) WithState(ctx context.Context, objId dagql.IDType, srv *dagql.Server) (*Llm, error) {
	obj, err := srv.Load(ctx, objId.ID())
	if err != nil {
		return nil, err
	}
	llm = llm.Clone()
	llm.state = obj
	llm.dirty = true
	return llm, nil
}

func (llm *Llm) State(ctx context.Context, dag *dagql.Server) (dagql.Typed, error) {
	llm, err := llm.Sync(ctx, dag)
	if err != nil {
		return nil, err
	}
	return llm.state, nil
}

// Add this method to the Message type
// TODO: shall this be provider specific ?
func (msg ModelMessage) Text() (string, error) {
	switch v := msg.Content.(type) {
	case string:
		return v, nil
	case []interface{}:
		if len(v) > 0 {
			if text, ok := v[0].(map[string]interface{})["text"].(string); ok {
				return text, nil
			}
		}
	}
	return "", fmt.Errorf("unable to extract text from message content: %v", msg.Content)
}

type LlmMiddleware struct {
	Server *dagql.Server
}

// We don't expose these types to modules SDK codegen, but
// we still want their graphql schemas to be available for
// internal usage. So we use this list to scrub them from
// the introspection JSON that module SDKs use for codegen.
var TypesHiddenFromModuleSDKs = []dagql.Typed{
	&Host{},

	&Engine{},
	&EngineCache{},
	&EngineCacheEntry{},
	&EngineCacheEntrySet{},
}

func (s LlmMiddleware) extendLlmType(targetType dagql.ObjectType) error {
	llmType, ok := s.Server.ObjectType(new(Llm).Type().Name())
	if !ok {
		return fmt.Errorf("failed to lookup llm type")
	}
	idType, ok := targetType.IDType()
	if !ok {
		return fmt.Errorf("failed to lookup ID type for %T", targetType)
	}
	typename := targetType.TypeName()
	// Install with<targetType>()
	llmType.Extend(
		dagql.FieldSpec{
			Name:        "with" + typename,
			Description: fmt.Sprintf("Set the llm state to a %s", typename),
			Type:        llmType.Typed(),
			Args: dagql.InputSpecs{
				{
					Name:        "value",
					Description: fmt.Sprintf("The value of the %s to save", typename),
					Type:        idType,
				},
			},
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			llm := self.(dagql.Instance[*Llm]).Self
			id := args["value"].(dagql.IDType)
			return llm.WithState(ctx, id, s.Server)
		},
		nil,
	)
	// Install <targetType>()
	llmType.Extend(
		dagql.FieldSpec{
			Name:        typename,
			Description: fmt.Sprintf("Retrieve the llm state as a %s", typename),
			Type:        targetType.Typed(),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			llm := self.(dagql.Instance[*Llm]).Self
			return llm.State(ctx, s.Server)
		},
		nil,
	)
	return nil
}

func (s LlmMiddleware) InstallObject(targetType dagql.ObjectType, install func(dagql.ObjectType)) {
	install(targetType)
	typename := targetType.TypeName()
	if strings.HasPrefix(typename, "_") {
		return
	}

	// don't extend LLM for types that we hide from modules, lest the codegen yield a
	// WithEngine(*Engine) that refers to an unknown *Engine type.
	//
	// FIXME: in principle LLM should be able to refer to these types, so this should
	// probably be moved to codegen somehow, i.e. if a field refers to a type that is
	// hidden, don't codegen the field.
	for _, hiddenType := range TypesHiddenFromModuleSDKs {
		if hiddenType.Type().Name() == typename {
			return
		}
	}

	if err := s.extendLlmType(targetType); err != nil {
		panic(err)
	}
}

func (s LlmMiddleware) ModuleWithObject(ctx context.Context, mod *Module, targetTypedef *TypeDef) (*Module, error) {
	// Install the target type
	mod, err := mod.WithObject(ctx, targetTypedef)
	if err != nil {
		return nil, err
	}
	typename := targetTypedef.Type().Name()
	targetType, ok := s.Server.ObjectType(typename)
	if !ok {
		return nil, fmt.Errorf("can't retrieve object type %s", typename)
	}
	if err := s.extendLlmType(targetType); err != nil {
		return nil, err
	}
	return mod, nil
}
