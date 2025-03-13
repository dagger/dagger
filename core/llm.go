package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
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

	maxAPICalls int
	apiCalls    int
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
	Content    string
	ToolCalls  []ToolCall
	TokenUsage TokenUsage
}

type TokenUsage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

// ModelMessage represents a generic message in the LLM conversation
type ModelMessage struct {
	Role        string     `json:"role"`
	Content     any        `json:"content"`
	ToolCalls   []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID  string     `json:"tool_call_id,omitempty"`
	ToolErrored bool       `json:"tool_errored,omitempty"`
	TokenUsage  TokenUsage `json:"token_usage,omitempty"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Function FuncCall `json:"function"`
	Type     string   `json:"type"`
}

type FuncCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
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
	AnthropicAPIKey  string
	AnthropicBaseURL string
	AnthropicModel   string

	OpenAIAPIKey       string
	OpenAIAzureVersion string
	OpenAIBaseURL      string
	OpenAIModel        string

	GeminiAPIKey  string
	GeminiBaseURL string
	GeminiModel   string
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

func (r *LlmRouter) routeAnthropicModel() *LlmEndpoint {
	defaultSystemPrompt := "You are a helpful AI assistant. You can use tools to accomplish the user's requests"
	endpoint := &LlmEndpoint{
		BaseURL:  r.AnthropicBaseURL,
		Key:      r.AnthropicAPIKey,
		Provider: Anthropic,
	}
	endpoint.Client = newAnthropicClient(endpoint, defaultSystemPrompt)

	return endpoint
}

func (r *LlmRouter) routeOpenAIModel() *LlmEndpoint {
	endpoint := &LlmEndpoint{
		BaseURL:  r.OpenAIBaseURL,
		Key:      r.OpenAIAPIKey,
		Provider: OpenAI,
	}
	endpoint.Client = newOpenAIClient(endpoint, r.OpenAIAzureVersion)

	return endpoint
}

func (r *LlmRouter) routeGoogleModel() (*LlmEndpoint, error) {
	defaultSystemPrompt := "You are a helpful AI assistant. You can use tools to accomplish the user's requests"
	endpoint := &LlmEndpoint{
		BaseURL:  r.GeminiBaseURL,
		Key:      r.GeminiAPIKey,
		Provider: Google,
	}
	client, err := newGenaiClient(endpoint, defaultSystemPrompt)
	if err != nil {
		return nil, err
	}
	endpoint.Client = client

	return endpoint, nil
}

func (r *LlmRouter) routeOtherModel() *LlmEndpoint {
	// default to openAI compat from other providers
	endpoint := &LlmEndpoint{
		BaseURL:  r.OpenAIBaseURL,
		Key:      r.OpenAIAPIKey,
		Provider: Other,
	}
	endpoint.Client = newOpenAIClient(endpoint, r.OpenAIAzureVersion)

	return endpoint
}

// Return a default model, if configured
func (r *LlmRouter) DefaultModel() string {
	for _, model := range []string{r.OpenAIModel, r.AnthropicModel, r.GeminiModel} {
		if model != "" {
			return model
		}
	}
	if r.OpenAIAPIKey != "" {
		return "gpt-4o"
	}
	if r.AnthropicAPIKey != "" {
		return anthropic.ModelClaude3_5SonnetLatest
	}
	if r.OpenAIBaseURL != "" {
		return "llama-3.2"
	}
	if r.GeminiAPIKey != "" {
		return "gemini-2.0-flash"
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
		googleEndpoint, err := r.routeGoogleModel()
		if err != nil {
			return nil, err
		}
		endpoint = googleEndpoint
	} else if r.isMistralModel(model) {
		return nil, fmt.Errorf("mistral models are not yet supported")
	} else {
		endpoint = r.routeOtherModel()
	}
	endpoint.Model = model
	return endpoint, nil
}

func (r *LlmRouter) LoadConfig(ctx context.Context, getenv func(context.Context, string) (string, error)) error {
	if getenv == nil {
		getenv = func(ctx context.Context, key string) (string, error) {
			return os.Getenv(key), nil
		}
	}
	var err error
	r.AnthropicAPIKey, err = getenv(ctx, "ANTHROPIC_API_KEY")
	if err != nil {
		return err
	}
	r.AnthropicBaseURL, err = getenv(ctx, "ANTHROPIC_BASE_URL")
	if err != nil {
		return err
	}
	r.AnthropicModel, err = getenv(ctx, "ANTHROPIC_MODEL")
	if err != nil {
		return err
	}
	r.OpenAIAPIKey, err = getenv(ctx, "OPENAI_API_KEY")
	if err != nil {
		return err
	}
	r.OpenAIAzureVersion, err = getenv(ctx, "OPENAI_AZURE_VERSION")
	if err != nil {
		return err
	}
	r.OpenAIBaseURL, err = getenv(ctx, "OPENAI_BASE_URL")
	if err != nil {
		return err
	}
	r.OpenAIModel, err = getenv(ctx, "OPENAI_MODEL")
	if err != nil {
		return err
	}
	r.GeminiAPIKey, err = getenv(ctx, "GEMINI_API_KEY")
	if err != nil {
		return err
	}
	r.GeminiBaseURL, err = getenv(ctx, "GEMINI_BASE_URL")
	if err != nil {
		return err
	}
	r.GeminiModel, err = getenv(ctx, "GEMINI_MODEL")
	if err != nil {
		return err
	}
	return nil
}

func NewLlmRouter(ctx context.Context, srv *dagql.Server) (_ *LlmRouter, rerr error) {
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
	ctx, span := Tracer(ctx).Start(ctx, "load LLM router config", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.End(span, func() error { return rerr })
	env := make(map[string]string)
	// Load .env from current directory, if it exists
	if envFile, err := loadSecret(ctx, "file://.env"); err == nil {
		if e, err := godotenv.Unmarshal(envFile); err == nil {
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

func NewLlm(ctx context.Context, query *Query, srv *dagql.Server, model string, maxAPICalls int) (*Llm, error) {
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
		return nil, fmt.Errorf("no valid LLM endpoint configuration")
	}
	return &Llm{
		Query:       query,
		Endpoint:    endpoint,
		maxAPICalls: maxAPICalls,
		calls:       make(map[string]string),
		// FIXME: support multiple variables in state
		// state:  make(map[string]dagql.Typed),
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

func (llm *Llm) WithModel(ctx context.Context, model string, srv *dagql.Server) (*Llm, error) {
	llm = llm.Clone()
	router, err := NewLlmRouter(ctx, srv)
	if err != nil {
		return nil, err
	}
	endpoint, err := router.Route(model)
	if err != nil {
		return nil, err
	}
	if endpoint.Model == "" {
		return nil, fmt.Errorf("no valid LLM endpoint configuration")
	}
	llm.Endpoint = endpoint
	return llm, nil
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
		if llm.maxAPICalls > 0 && llm.apiCalls >= llm.maxAPICalls {
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
			Role:       "assistant",
			Content:    res.Content,
			ToolCalls:  res.ToolCalls,
			TokenUsage: res.TokenUsage,
		})
		// Handle tool calls
		// calls := res.Choices[0].Message.ToolCalls
		if len(res.ToolCalls) == 0 {
			break
		}
		for _, toolCall := range res.ToolCalls {
			for _, tool := range tools {
				if tool.Name == toolCall.Function.Name {
					result, isError := func() (string, bool) {
						ctx, span := Tracer(ctx).Start(ctx,
							fmt.Sprintf("🤖 💻 %s", toolCall.Function.Name),
							telemetry.Passthrough(),
							telemetry.Reveal())
						defer span.End()
						result, err := tool.Call(ctx, toolCall.Function.Arguments)
						if err != nil {
							// If the BBI driver itself returned an error,
							// send that error to the model
							span.SetStatus(codes.Error, err.Error())

							errResponse := err.Error()

							// propagate error values to the model
							if extErr, ok := err.(dagql.ExtendedError); ok {
								var exts []string
								for k, v := range extErr.Extensions() {
									var ext strings.Builder
									fmt.Fprintf(&ext, "<%s>\n", k)

									switch v := v.(type) {
									case string:
										ext.WriteString(v)
									default:
										jsonBytes, err := json.Marshal(v)
										if err != nil {
											fmt.Fprintf(&ext, "error marshalling value: %s", err.Error())
										} else {
											ext.Write(jsonBytes)
										}
									}

									fmt.Fprintf(&ext, "\n</%s>", k)

									exts = append(exts, ext.String())
								}
								if len(exts) > 0 {
									sort.Strings(exts)
									errResponse += "\n\n" + strings.Join(exts, "\n\n")
								}
							}

							return errResponse, true
						}
						stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
						defer stdio.Close()
						switch v := result.(type) {
						case string:
							fmt.Fprint(stdio.Stdout, v)
							return v, false
						default:
							jsonBytes, err := json.Marshal(v)
							if err != nil {
								span.SetStatus(codes.Error, err.Error())
								return fmt.Sprintf("error processing tool result: %s", err.Error()), true
							}
							fmt.Fprint(stdio.Stdout, string(jsonBytes))
							return string(jsonBytes), false
						}
					}()
					func() {
						llm.calls[toolCall.ID] = result
						llm.history = append(llm.history, ModelMessage{
							Role:        "user", // Anthropic only allows tool calls in user messages
							Content:     result,
							ToolCallID:  toolCall.ID,
							ToolErrored: isError,
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

func (llm *Llm) WithState(ctx context.Context, objID dagql.IDType, srv *dagql.Server) (*Llm, error) {
	obj, err := srv.Load(ctx, objID.ID())
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

type LlmHook struct {
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

func (s LlmHook) ExtendLlmType(targetType dagql.ObjectType) error {
	llmType, ok := s.Server.ObjectType(new(Llm).Type().Name())
	if !ok {
		return fmt.Errorf("failed to lookup llm type")
	}
	idType, ok := targetType.IDType()
	if !ok {
		return fmt.Errorf("failed to lookup ID type for %T", targetType)
	}
	typename := targetType.TypeName()
	// Install with<TargetType>()
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
		dagql.CacheSpec{},
	)
	// Install <targetType>()
	llmType.Extend(
		dagql.FieldSpec{
			Name:        gqlFieldName(typename),
			Description: fmt.Sprintf("Retrieve the llm state as a %s", typename),
			Type:        targetType.Typed(),
		},
		func(ctx context.Context, self dagql.Object, args map[string]dagql.Input) (dagql.Typed, error) {
			llm := self.(dagql.Instance[*Llm]).Self
			return llm.State(ctx, s.Server)
		},
		dagql.CacheSpec{},
	)
	return nil
}

func (s LlmHook) InstallObject(targetType dagql.ObjectType) {
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

	if err := s.ExtendLlmType(targetType); err != nil {
		panic(err)
	}
}

func (s LlmHook) ModuleWithObject(ctx context.Context, mod *Module, targetTypedef *TypeDef) (*Module, error) {
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
	if err := s.ExtendLlmType(targetType); err != nil {
		return nil, err
	}
	return mod, nil
}
