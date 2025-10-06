package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/cenkalti/backoff/v4"
	"github.com/iancoleman/strcase"
	"github.com/joho/godotenv"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
)

func init() {
	strcase.ConfigureAcronym("LLM", "LLM")
}

const (
	modelDefaultAnthropic = string(anthropic.ModelClaudeSonnet4_5)
	modelDefaultGoogle    = "gemini-2.5-flash"
	modelDefaultOpenAI    = "gpt-4.1"
	modelDefaultMeta      = "llama-3.2"
	modelDefaultMistral   = "mistral-7b-instruct"
)

func resolveModelAlias(maybeAlias string) string {
	switch maybeAlias {
	case "anthropic", "claude":
		return modelDefaultAnthropic
	case "google", "gemini":
		return modelDefaultGoogle
	case "openai", "gpt":
		return modelDefaultOpenAI
	case "meta", "llama":
		return modelDefaultMeta
	case "mistral":
		return modelDefaultMistral
	default:
		// not a recognized alias
		return maybeAlias
	}
}

// An instance of a LLM (large language model), with its state and tool calling environment
type LLM struct {
	// TODO: document default behavior
	SystemPrompt string `field:"true" doc:"A system prompt to send."`

	Messages []*LLMMessage `field:"true" doc:"The full message history."`

	// The environment accessible to the LLM, exposed over MCP
	mcp *MCP

	maxAPICalls int
	apiCalls    int

	model string

	endpoint    *LLMEndpoint
	endpointMtx *sync.Mutex

	syncOneStep bool

	// Whether to disable the default system prompt
	disableDefaultSystemPrompt bool
}

type LLMEndpoint struct {
	Model    string
	BaseURL  string
	Key      string
	Provider LLMProvider
	Client   LLMClient
}

type LLMProvider string

// LLMClient interface defines the methods that each provider must implement
type LLMClient interface {
	SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool) (*LLMResponse, error)
	IsRetryable(err error) bool
}

type LLMResponse struct {
	Content    string
	ToolCalls  []*LLMToolCall
	TokenUsage LLMTokenUsage
}

type LLMTokenUsage struct {
	InputTokens       int64 `field:"true" json:"input_tokens"`
	OutputTokens      int64 `field:"true" json:"output_tokens"`
	CachedTokenReads  int64 `field:"true" json:"cached_token_reads"`
	CachedTokenWrites int64 `field:"true" json:"cached_token_writes"`
	TotalTokens       int64 `field:"true" json:"total_tokens"`
}

func (*LLMTokenUsage) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMTokenUsage",
		NonNull:   true,
	}
}

// LLMMessage represents a generic message in the LLM conversation
type LLMMessage struct {
	Role        LLMMessageRole `field:"true" json:"role"`
	Content     string         `field:"true" json:"content"`
	ToolCalls   []*LLMToolCall `field:"true" json:"tool_calls,omitempty"`
	ToolCallID  string         `field:"true" json:"tool_call_id,omitempty"`
	ToolErrored bool           `field:"true" json:"tool_errored,omitempty"`

	// NB: this isn't exposed as a field, since it will only be present on
	// response messages, but shamefully initially because it's annoying to make
	// it a pointer
	TokenUsage LLMTokenUsage `json:"token_usage,omitzero"`
}

func (*LLMMessage) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMMessage",
		NonNull:   true,
	}
}

type LLMMessageRole string

var LLMMessageRoles = dagql.NewEnum[LLMMessageRole]()

var (
	LLMMessageRoleUser      = LLMMessageRoles.Register("LLM_ROLE_USER", "A user prompt or tool response.")
	LLMMessageRoleAssistant = LLMMessageRoles.Register("LLM_ROLE_ASSISTANT", "A reply from the model.")
	LLMMessageRoleSystem    = LLMMessageRoles.Register("LLM_ROLE_SYSTEM", "A system prompt.")
)

func (LLMMessageRole) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMMessageRole",
		NonNull:   true,
	}
}

func (mode LLMMessageRole) TypeDescription() string {
	return "The role that generated a message."
}

func (mode LLMMessageRole) Decoder() dagql.InputDecoder {
	return LLMMessageRoles
}

func (mode LLMMessageRole) ToLiteral() call.Literal {
	return LLMMessageRoles.Literal(mode)
}

func (role LLMMessageRole) String() string {
	return string(role)
}

type LLMToolCall struct {
	CallID    string `field:"true" json:"id"`
	Name      string `field:"true" json:"name"`
	Arguments JSON   `field:"true" json:"arguments"`
}

func (*LLMToolCall) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMToolCall",
		NonNull:   true,
	}
}

const (
	OpenAI    LLMProvider = "openai"
	Anthropic LLMProvider = "anthropic"
	Google    LLMProvider = "google"
	Meta      LLMProvider = "meta"
	Mistral   LLMProvider = "mistral"
	DeepSeek  LLMProvider = "deepseek"
	Other     LLMProvider = "other"
)

// A LLM routing configuration
type LLMRouter struct {
	AnthropicAPIKey  string
	AnthropicBaseURL string
	AnthropicModel   string

	OpenAIAPIKey           string
	OpenAIAzureVersion     string
	OpenAIBaseURL          string
	OpenAIModel            string
	OpenAIDisableStreaming bool

	GeminiAPIKey  string
	GeminiBaseURL string
	GeminiModel   string
}

func (r *LLMRouter) isAnthropicModel(model string) bool {
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "anthropic/")
}

func (r *LLMRouter) isOpenAIModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "openai/")
}

func (r *LLMRouter) isGoogleModel(model string) bool {
	return strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "google/")
}

func (r *LLMRouter) isMistralModel(model string) bool {
	return strings.HasPrefix(model, "mistral-") || strings.HasPrefix(model, "mistral/")
}

func (r *LLMRouter) isReplay(model string) bool {
	return strings.HasPrefix(model, "replay-") || strings.HasPrefix(model, "replay/")
}

func (r *LLMRouter) getReplay(model string) (messages []*LLMMessage, _ error) {
	model, ok := strings.CutPrefix(model, "replay-")
	if !ok {
		model, ok = strings.CutPrefix(model, "replay/")
		if !ok {
			return nil, fmt.Errorf("model %q is not replayable", model)
		}
	}

	result, err := base64.StdEncoding.DecodeString(model)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(result, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (r *LLMRouter) routeAnthropicModel() *LLMEndpoint {
	endpoint := &LLMEndpoint{
		BaseURL:  r.AnthropicBaseURL,
		Key:      r.AnthropicAPIKey,
		Provider: Anthropic,
	}
	endpoint.Client = newAnthropicClient(endpoint)

	return endpoint
}

func (r *LLMRouter) routeOpenAIModel() *LLMEndpoint {
	endpoint := &LLMEndpoint{
		BaseURL:  r.OpenAIBaseURL,
		Key:      r.OpenAIAPIKey,
		Provider: OpenAI,
	}
	endpoint.Client = newOpenAIClient(endpoint, r.OpenAIAzureVersion, r.OpenAIDisableStreaming)

	return endpoint
}

func (r *LLMRouter) routeGoogleModel() (*LLMEndpoint, error) {
	endpoint := &LLMEndpoint{
		BaseURL:  r.GeminiBaseURL,
		Key:      r.GeminiAPIKey,
		Provider: Google,
	}
	client, err := newGenaiClient(endpoint)
	if err != nil {
		return nil, err
	}
	endpoint.Client = client

	return endpoint, nil
}

func (r *LLMRouter) routeOtherModel() *LLMEndpoint {
	// default to openAI compat from other providers
	endpoint := &LLMEndpoint{
		BaseURL:  r.OpenAIBaseURL,
		Key:      r.OpenAIAPIKey,
		Provider: Other,
	}
	endpoint.Client = newOpenAIClient(endpoint, r.OpenAIAzureVersion, r.OpenAIDisableStreaming)

	return endpoint
}

func (r *LLMRouter) routeReplayModel(model string) (*LLMEndpoint, error) {
	replay, err := r.getReplay(model)
	if err != nil {
		return nil, err
	}
	endpoint := &LLMEndpoint{}
	endpoint.Client = newHistoryReplay(replay)
	return endpoint, nil
}

// Return a default model, if configured
func (r *LLMRouter) DefaultModel() string {
	for _, model := range []string{r.OpenAIModel, r.AnthropicModel, r.GeminiModel} {
		if model != "" {
			return model
		}
	}
	if r.OpenAIAPIKey != "" {
		return modelDefaultOpenAI
	}
	if r.AnthropicAPIKey != "" {
		return modelDefaultAnthropic
	}
	if r.OpenAIBaseURL != "" {
		return modelDefaultMeta
	}
	if r.GeminiAPIKey != "" {
		return modelDefaultGoogle
	}
	return ""
}

// Return an endpoint for the requested model
// If the model name is not set, a default will be selected.
func (r *LLMRouter) Route(model string) (*LLMEndpoint, error) {
	if model == "" {
		model = r.DefaultModel()
	} else {
		model = resolveModelAlias(model)
	}
	var endpoint *LLMEndpoint
	var err error
	switch {
	case r.isAnthropicModel(model):
		endpoint = r.routeAnthropicModel()
	case r.isOpenAIModel(model):
		endpoint = r.routeOpenAIModel()
	case r.isGoogleModel(model):
		endpoint, err = r.routeGoogleModel()
		if err != nil {
			return nil, err
		}
	case r.isMistralModel(model):
		return nil, fmt.Errorf("mistral models are not yet supported")
	case r.isReplay(model):
		endpoint, err = r.routeReplayModel(model)
		if err != nil {
			return nil, err
		}
	default:
		endpoint = r.routeOtherModel()
	}
	endpoint.Model = model
	return endpoint, nil
}

func (r *LLMRouter) LoadConfig(ctx context.Context, getenv func(context.Context, string) (string, error)) error {
	if getenv == nil {
		getenv = func(_ context.Context, key string) (string, error) { //nolint:unparam
			return os.Getenv(key), nil
		}
	}

	save := func(key string, dest *string) error {
		value, err := getenv(ctx, key)
		if err != nil {
			return fmt.Errorf("get %q: %w", key, err)
		}
		if value != "" {
			*dest = value
		}
		return nil
	}

	var eg errgroup.Group
	eg.Go(func() error {
		return save("ANTHROPIC_API_KEY", &r.AnthropicAPIKey)
	})
	eg.Go(func() error {
		return save("ANTHROPIC_BASE_URL", &r.AnthropicBaseURL)
	})
	eg.Go(func() error {
		return save("ANTHROPIC_MODEL", &r.AnthropicModel)
	})

	eg.Go(func() error {
		return save("OPENAI_API_KEY", &r.OpenAIAPIKey)
	})
	eg.Go(func() error {
		return save("OPENAI_AZURE_VERSION", &r.OpenAIAzureVersion)
	})
	eg.Go(func() error {
		return save("OPENAI_BASE_URL", &r.OpenAIBaseURL)
	})
	eg.Go(func() error {
		return save("OPENAI_MODEL", &r.OpenAIModel)
	})

	eg.Go(func() error {
		return save("GEMINI_API_KEY", &r.GeminiAPIKey)
	})
	eg.Go(func() error {
		return save("GEMINI_BASE_URL", &r.GeminiBaseURL)
	})
	eg.Go(func() error {
		return save("GEMINI_MODEL", &r.GeminiModel)
	})

	var (
		openAIDisableStreaming string
	)
	eg.Go(func() error {
		var err error
		openAIDisableStreaming, err = getenv(ctx, "OPENAI_DISABLE_STREAMING")
		return err
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	if openAIDisableStreaming != "" {
		v, err := strconv.ParseBool(openAIDisableStreaming)
		if err != nil {
			return err
		}
		r.OpenAIDisableStreaming = v
	}

	return nil
}

func NewLLMRouter(ctx context.Context, srv *dagql.Server) (_ *LLMRouter, rerr error) {
	router := new(LLMRouter)
	// Get the secret plaintext, from either a URI (provider lookup) or a plaintext (no-op)
	loadSecret := func(ctx context.Context, uriOrPlaintext string) (string, error) {
		if _, _, err := secretprovider.ResolverForID(uriOrPlaintext); err == nil {
			var result string
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

func (q *Query) NewLLM(ctx context.Context, model string, maxAPICalls int) (*LLM, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var env dagql.ObjectResult[*Env]
	if err := srv.Select(ctx, srv.Root(), &env, dagql.Selector{
		Field: "env",
	}); err != nil {
		return nil, err
	}
	return &LLM{
		model:       model,
		maxAPICalls: maxAPICalls,
		mcp:         newMCP(env),
		endpointMtx: &sync.Mutex{},
	}, nil
}

func (llm *LLM) WithStaticTools() *LLM {
	llm = llm.Clone()
	llm.mcp.staticTools = true
	return llm
}

// loadLLMRouter creates an LLM router that routes to the root client
func loadLLMRouter(ctx context.Context, query *Query) (*LLMRouter, error) {
	parentClient, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, err
	}
	ctx = engine.ContextWithClientMetadata(ctx, parentClient)
	mainSrv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, err
	}
	return NewLLMRouter(ctx, mainSrv)
}

func (*LLM) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLM",
		NonNull:   true,
	}
}

func (llm *LLM) Clone() *LLM {
	cp := *llm
	cp.Messages = slices.Clone(cp.Messages)
	cp.mcp = cp.mcp.Clone()
	cp.endpoint = llm.endpoint
	cp.endpointMtx = &sync.Mutex{}
	return &cp
}

func (llm *LLM) Endpoint(ctx context.Context) (*LLMEndpoint, error) {
	llm.endpointMtx.Lock()
	defer llm.endpointMtx.Unlock()

	if llm.endpoint != nil {
		return llm.endpoint, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	router, err := loadLLMRouter(ctx, query)
	if err != nil {
		return nil, err
	}
	endpoint, err := router.Route(llm.model)
	if err != nil {
		return nil, err
	}
	if endpoint.Model == "" {
		return nil, fmt.Errorf("no valid LLM endpoint configuration")
	}

	llm.endpoint = endpoint

	return llm.endpoint, nil
}

// Generate a human-readable documentation of tools available to the model
func (llm *LLM) ToolsDoc(ctx context.Context) (string, error) {
	tools, err := llm.mcp.Tools(ctx)
	if err != nil {
		return "", err
	}
	var result string
	for _, tool := range tools {
		schema, err := json.MarshalIndent(tool.Schema, "", "  ")
		if err != nil {
			return "", err
		}
		result = fmt.Sprintf("%s## %s\n\n%s\n\n%s\n\n", result, tool.Name, tool.Description, string(schema))
	}
	return result, nil
}

func (llm *LLM) WithModel(model string) *LLM {
	llm = llm.Clone()
	llm.model = model

	llm.endpointMtx.Lock()
	defer llm.endpointMtx.Unlock()
	llm.endpoint = nil

	return llm
}

// Append a user message (prompt) to the message history
func (llm *LLM) WithPrompt(
	// The prompt message.
	prompt string,
) *LLM {
	prompt = os.Expand(prompt, func(key string) string {
		if binding, found := llm.mcp.env.Self().Input(key); found {
			return binding.String()
		}
		// leave unexpanded, perhaps it refers to an object var
		return fmt.Sprintf("$%s", key)
	})
	llm = llm.Clone()
	llm.Messages = append(llm.Messages, &LLMMessage{
		Role:    "user",
		Content: prompt,
	})
	return llm
}

// WithPromptFile is like WithPrompt but reads the prompt from a file
func (llm *LLM) WithPromptFile(ctx context.Context, file *File) (*LLM, error) {
	contents, err := file.Contents(ctx, nil, nil)
	if err != nil {
		return nil, err
	}
	return llm.WithPrompt(string(contents)), nil
}

// Append a system prompt message to the history
func (llm *LLM) WithSystemPrompt(prompt string) *LLM {
	llm = llm.Clone()
	llm.Messages = append(llm.Messages, &LLMMessage{
		Role:    "system",
		Content: prompt,
	})
	return llm
}

// Append an assistant response message to the history
func (llm *LLM) WithResponse(content string, tokenUsage LLMTokenUsage) *LLM {
	llm = llm.Clone()
	llm.Messages = append(llm.Messages, &LLMMessage{
		Role:       "assistant",
		Content:    content,
		TokenUsage: tokenUsage,
	})
	return llm
}

// Append a tool call to the last assistant message in the history
func (llm *LLM) WithToolCall(callID, tool string, arguments JSON) *LLM {
	llm = llm.Clone()
	// Find the last assistant message and append the tool call to it
	for i := len(llm.Messages) - 1; i >= 0; i-- {
		if llm.Messages[i].Role == "assistant" {
			llm.Messages[i].ToolCalls = append(llm.Messages[i].ToolCalls, &LLMToolCall{
				CallID:    callID,
				Name:      tool,
				Arguments: arguments,
			})
			break
		}
	}
	return llm
}

// Append a tool response (user) message to the history
func (llm *LLM) WithToolResponse(callID, content string, errored bool) *LLM {
	llm = llm.Clone()
	llm.Messages = append(llm.Messages, &LLMMessage{
		Role:        "user",
		Content:     content,
		ToolCallID:  callID,
		ToolErrored: errored,
	})
	return llm
}

// Disable the default system prompt
func (llm *LLM) WithoutDefaultSystemPrompt() *LLM {
	llm = llm.Clone()
	llm.disableDefaultSystemPrompt = true
	return llm
}

// Disable the default system prompt
func (llm *LLM) WithBlockedFunction(ctx context.Context, typeName, funcName string) (*LLM, error) {
	llm = llm.Clone()
	if err := llm.mcp.BlockFunction(ctx, typeName, funcName); err != nil {
		return nil, err
	}
	return llm, nil
}

// Add an external MCP server to the LLM
func (llm *LLM) WithMCPServer(name string, svc dagql.ObjectResult[*Service]) *LLM {
	llm = llm.Clone()
	llm.mcp = llm.mcp.WithMCPServer(&MCPServerConfig{
		Name:    name,
		Service: svc,
	})
	return llm
}

// Return the last message sent by the agent
func (llm *LLM) LastReply(ctx context.Context) (string, error) {
	var reply string = "(no reply)"
	for _, msg := range llm.Messages {
		if msg.Role != "assistant" {
			continue
		}
		txt := msg.Content
		if len(txt) == 0 {
			continue
		}
		reply = txt
	}
	return reply, nil
}

func (llm *LLM) messagesWithSystemPrompt() []*LLMMessage {
	var systemPrompt string
	if !llm.disableDefaultSystemPrompt {
		systemPrompt = llm.mcp.DefaultSystemPrompt()
	}
	if systemPrompt != "" {
		return append([]*LLMMessage{{
			Role:    "system",
			Content: systemPrompt,
		}}, llm.Messages...)
	}
	return llm.Messages
}

type ModelFinishedError struct {
	Reason string
}

func (err *ModelFinishedError) Error() string {
	return fmt.Sprintf("model finished: %s", err.Reason)
}

// Send configures the LLM to only evaluate one step when syncing.
func (llm *LLM) Step(ctx context.Context, inst dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	origEnv := llm.Env()

	llm = llm.Clone()

	b := backoff.NewExponentialBackOff()
	// Sane defaults (ideally not worth extra knobs)
	b.InitialInterval = 1 * time.Second
	b.MaxInterval = 30 * time.Second
	b.MaxElapsedTime = 2 * time.Minute

	tools, err := llm.mcp.Tools(ctx)
	if err != nil {
		return inst, err
	}

	messagesToSend := llm.messagesWithSystemPrompt()

	var newMessages []*LLMMessage
	for _, msg := range slices.Backward(messagesToSend) {
		if msg.Role == "assistant" || msg.ToolCallID != "" {
			// only display messages appended since the last response
			break
		}
		newMessages = append(newMessages, msg)
	}
	slices.Reverse(newMessages)
	for _, msg := range newMessages {
		func() {
			var emoji string
			switch msg.Role {
			case LLMMessageRoleUser:
				emoji = "🧑"
			case LLMMessageRoleSystem:
				emoji = "⚙️"
			}
			ctx, span := Tracer(ctx).Start(ctx, "LLM prompt",
				telemetry.Reveal(),
				trace.WithAttributes(
					attribute.String(telemetry.UIActorEmojiAttr, emoji),
					attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageSent),
					attribute.String(telemetry.LLMRoleAttr, msg.Role.String()),
					attribute.Bool(telemetry.UIInternalAttr, msg.Role == LLMMessageRoleSystem),
				))
			defer span.End()
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
				log.String(telemetry.ContentTypeAttr, "text/markdown"))
			defer stdio.Close()
			fmt.Fprint(stdio.Stdout, msg.Content)
		}()
	}

	var res *LLMResponse

	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return inst, err
	}
	client := ep.Client
	err = backoff.Retry(func() error {
		var sendErr error
		ctx, span := Tracer(ctx).Start(ctx, "LLM query", telemetry.Reveal(), trace.WithAttributes(
			attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
			attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
			attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
		))
		res, sendErr = client.SendQuery(ctx, messagesToSend, tools)
		telemetry.End(span, func() error { return sendErr })
		if sendErr != nil {
			var finished *ModelFinishedError
			if errors.As(sendErr, &finished) {
				// Don't retry if the model finished explicitly, treat as permanent.
				return backoff.Permanent(sendErr)
			}
			if !client.IsRetryable(sendErr) {
				// Maybe an invalid request - give up.
				return backoff.Permanent(sendErr)
			}
			// Log retry attempts? Maybe with increasing severity?
			// For now, just return the error to signal backoff to retry.
			return sendErr
		}
		// Success, stop retrying
		return nil
	}, backoff.WithContext(b, ctx))
	if err != nil {
		return inst, err
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	var sels []dagql.Selector
	if res.Content != "" {
		sels = append(sels, dagql.Selector{
			Field: "withResponse",
			Args: []dagql.NamedInput{
				{
					Name:  "content",
					Value: dagql.NewString(res.Content),
				},
			},
		})
	}
	for _, call := range res.ToolCalls {
		sels = append(sels, dagql.Selector{
			Field: "withToolCall",
			Args: []dagql.NamedInput{
				{
					Name:  "callID",
					Value: dagql.NewString(call.CallID),
				},
				{
					Name:  "tool",
					Value: dagql.NewString(call.Name),
				},
				{
					Name:  "arguments",
					Value: call.Arguments,
				},
			},
		})
	}
	for _, msg := range llm.mcp.CallBatch(ctx, tools, res.ToolCalls) {
		sels = append(sels, dagql.Selector{
			Field: "withToolResponse",
			Args: []dagql.NamedInput{
				{
					Name:  "callID",
					Value: dagql.NewString(msg.ToolCallID),
				},
				{
					Name:  "content",
					Value: dagql.NewString(msg.Content),
				},
				{
					Name:  "errored",
					Value: dagql.NewBoolean(msg.ToolErrored),
				},
			},
		})
	}

	// Persist any env changes
	if llm.Env().ID().Digest() != origEnv.ID().Digest() {
		sels = append(sels, dagql.Selector{
			Field: "withEnv",
			Args: []dagql.NamedInput{
				{
					Name:  "env",
					Value: dagql.NewID[*Env](llm.Env().ID()),
				},
			},
		})
	}

	var stepped dagql.ObjectResult[*LLM]
	err = srv.Select(ctx, inst, &stepped, sels...)
	if err != nil {
		return inst, err
	}

	return stepped, nil
}

// Loop sends the context to the LLM endpoint, processes replies and tool calls; continues in a loop
// Synchronize LLM state:
// 1. Send context to LLM endpoint
// 2. Process replies and tool calls
// 3. Continue in a loop until no tool calls, or caps are reached
func (llm *LLM) Loop(ctx context.Context, inst dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	if err := llm.allowed(ctx); err != nil {
		return inst, err
	}
	return llm.loop(ctx, inst)
}

func (llm *LLM) Interject(ctx context.Context) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return err
	}
	ctx, span := Tracer(ctx).Start(ctx, "LLM prompt", telemetry.Reveal(), trace.WithAttributes(
		attribute.String(telemetry.UIActorEmojiAttr, "🧑"),
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageSent),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleUser),
	))
	defer span.End()
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))
	defer stdio.Close()
	var lastAssistantMessage string
	for i := len(llm.Messages) - 1; i >= 0; i-- {
		if llm.Messages[i].Role == "assistant" {
			lastAssistantMessage = llm.Messages[i].Content
			break
		}
	}
	if lastAssistantMessage == "" {
		return fmt.Errorf("no message from assistant")
	}
	msg, err := bk.PromptHumanHelp(ctx, "LLM needs help!", fmt.Sprintf("The LLM was unable to complete its task and needs a prompt to continue. Here is its last message:\n%s", mdQuote(lastAssistantMessage)))
	if err != nil {
		return err
	}
	if msg == "" {
		return errors.New("no interjection provided; giving up")
	}
	fmt.Fprint(stdio.Stdout, msg)
	llm.Messages = append(llm.Messages, &LLMMessage{
		Role:    "user",
		Content: msg,
	})
	return nil
}

func mdQuote(msg string) string {
	lines := strings.Split(msg, "\n")
	for i, line := range lines {
		lines[i] = fmt.Sprintf("> %s", line)
	}
	return strings.Join(lines, "\n")
}

// autoInterject keeps the loop going if necessary, by prompting for a new
// input, adding it to the message history, and returning true
func (llm *LLM) autoInterject(ctx context.Context) (bool, error) {
	if llm.mcp.IsDone() {
		// we either didn't expect a return value, or got one - done!
		return false, nil
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return false, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return false, err
	}
	if !bk.Opts.Interactive {
		return false, nil
	}
	if err := llm.Interject(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (llm *LLM) loop(ctx context.Context, inst dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	if !llm.HasPrompt() {
		// dirty but no messages, possibly just a state change, nothing to do
		// until a prompt is given
		return inst, nil
	}

	for {
		if llm.maxAPICalls > 0 && llm.apiCalls >= llm.maxAPICalls {
			return inst, fmt.Errorf("reached API call limit: %d", llm.apiCalls)
		}
		llm.apiCalls++

		var err error
		inst, err = llm.Step(ctx, inst)
		if err != nil {
			var finished *ModelFinishedError
			if errors.As(err, &finished) {
				if interjected, interjectErr := llm.autoInterject(ctx); interjectErr != nil {
					// interjecting failed or was interrupted
					return inst, errors.Join(err, interjectErr)
				} else if interjected {
					// interjected - continue
					continue
				} else {
					// no interjection and none needed - we're just done
					break
				}
			}
			// Handle persistent error after all retries failed.
			return inst, err
		}
		if llm.mcp.Returned() {
			// we returned; exit the loop, since some models just keep going
			break
		}
	}
	return inst, nil
}

func (llm *LLM) HasPrompt() bool {
	return len(llm.Messages) > 0 && llm.Messages[len(llm.Messages)-1].Role == "user"
}

func (llm *LLM) allowed(ctx context.Context) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	module, err := query.CurrentModule(ctx)
	if err != nil {
		// allow non-module calls
		if errors.Is(err, ErrNoCurrentModule) {
			return nil
		}
		return fmt.Errorf("failed to figure out module while deciding if llm is allowed: %w", err)
	}

	src := module.ContextSource.Value.Self()
	if src.Kind != ModuleSourceKindGit {
		return nil
	}

	md, err := engine.ClientMetadataFromContext(ctx) // not mainclient
	if err != nil {
		return fmt.Errorf("llm sync failed fetching client metadata from context: %w", err)
	}

	moduleURL := src.Git.Symbolic
	for _, allowedModule := range md.AllowedLLMModules {
		if allowedModule == "all" || moduleURL == allowedModule {
			return nil
		}
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("llm sync failed fetching bk client for llm allow prompting: %w", err)
	}

	return bk.PromptAllowLLM(ctx, moduleURL)
}

func squash(str string) string {
	return strings.ReplaceAll(str, "\n", `\n`)
}

func (llm *LLM) History(ctx context.Context) ([]string, error) {
	var history []string
	var lastRole LLMMessageRole
	for _, msg := range llm.Messages {
		if len(history) > 0 && lastRole != msg.Role {
			// add a blank line when roles change
			history = append(history, "")
			lastRole = msg.Role
		}
		content := squash(msg.Content)
		switch msg.Role {
		case LLMMessageRoleUser:
			var item string
			if msg.ToolCallID != "" {
				item += "🛠️ 💬 "
			} else {
				item += "🧑 💬 "
			}
			if msg.ToolErrored {
				item += "ERROR: "
			}
			item += content
			history = append(history, item)
		case LLMMessageRoleAssistant:
			if len(content) > 0 {
				history = append(history, "🤖 💬 "+content)
			}
			for _, call := range msg.ToolCalls {
				item := fmt.Sprintf("🤖 🛠️ %s %s", call.Name, call.Arguments)
				history = append(history, item)
			}
		}
		if msg.TokenUsage.InputTokens > 0 || msg.TokenUsage.OutputTokens > 0 {
			history = append(history,
				fmt.Sprintf("🪙 Tokens Used: %d in => %d out",
					msg.TokenUsage.InputTokens,
					msg.TokenUsage.OutputTokens))
		}
	}
	return history, nil
}

func (llm *LLM) HistoryJSON(ctx context.Context) (JSON, error) {
	result, err := json.MarshalIndent(llm.Messages, "", "  ")
	if err != nil {
		return nil, err
	}
	return JSON(result), nil
}

func (llm *LLM) WithEnv(env dagql.ObjectResult[*Env]) *LLM {
	llm = llm.Clone()
	llm.mcp.env = env
	return llm
}

func (llm *LLM) Env() dagql.ObjectResult[*Env] {
	return llm.mcp.env
}

func (llm *LLM) BindResult(ctx context.Context, dag *dagql.Server, name string) (dagql.Nullable[*Binding], error) {
	var res dagql.Nullable[*Binding]
	if llm.mcp.LastResult() == nil {
		return res, nil
	}
	res.Value = &Binding{
		Key:          name,
		Value:        llm.mcp.LastResult(),
		ExpectedType: llm.mcp.LastResult().Type().Name(),
	}
	res.Valid = true
	return res, nil
}

func (llm *LLM) TokenUsage(ctx context.Context, dag *dagql.Server) (*LLMTokenUsage, error) {
	var res LLMTokenUsage
	for _, msg := range llm.Messages {
		res.InputTokens += msg.TokenUsage.InputTokens
		res.OutputTokens += msg.TokenUsage.OutputTokens
		res.CachedTokenReads += msg.TokenUsage.CachedTokenReads
		res.CachedTokenWrites += msg.TokenUsage.CachedTokenWrites
		res.TotalTokens += msg.TokenUsage.TotalTokens
	}
	return &res, nil
}
