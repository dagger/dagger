package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/cenkalti/backoff/v4"
	telemetry "github.com/dagger/otel-go"
	"github.com/iancoleman/strcase"
	"github.com/joho/godotenv"
	toml "github.com/pelletier/go-toml"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core/llmconfig"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
)

func init() {
	strcase.ConfigureAcronym("LLM", "LLM")
}

const (
	modelDefaultAnthropic = string(anthropic.ModelClaudeSonnet4_6)
	modelDefaultGoogle    = "gemini-2.5-flash"
	modelDefaultOpenAI    = "gpt-4.1"
	modelDefaultMeta      = "llama-3.2"
	modelDefaultMistral   = "mistral-7b-instruct"
	modelDefaultCodex     = "gpt-5.3-codex"

	// LLMCallDigestAttr is set on LLM prompt/response telemetry spans.
	// Its value is the DAG digest of the corresponding withPrompt or
	// withResponse call, enabling the TUI to branch from that point
	// in the conversation.
	LLMCallDigestAttr = "dagger.io/llm.call.digest"
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

	// OAuth fields for Claude Code subscription auth
	AuthToken        string // OAuth bearer token (used instead of Key when set)
	IsOAuth          bool   // Whether this endpoint uses OAuth authentication
	SubscriptionType string // "pro", "max", "team", "enterprise" (OAuth only)

	// Thinking / reasoning configuration
	ThinkingMode   string // "adaptive", "enabled", "disabled" (Anthropic) or reasoning effort (OpenAI)
	ThinkingBudget int64  // Max thinking tokens (Anthropic, when mode == "enabled")
}

type LLMProvider string

// LLMClient interface defines the methods that each provider must implement
type LLMClient interface {
	SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool) (*LLMResponse, error)
	IsRetryable(err error) bool
}

// LLMResponse is the internal result returned by a provider's SendQuery.
// It carries content blocks and token usage but is not exposed in the API;
// the evaluation loop converts it into LLMMessage history entries.
type LLMResponse struct {
	Content    []*LLMContentBlock
	TokenUsage LLMTokenUsage

	// DisplaySpans are the OTel spans created by the provider for
	// prompt/response display in the TUI (e.g., "LLM response",
	// "thinking"). Providers should NOT end these spans; the caller
	// (step) will set attributes and end them.
	DisplaySpans []trace.Span

	// ToolCallCtxs maps tool call IDs to the context of their display
	// span, so that tool execution spans are parented beneath them.
	ToolCallCtxs map[string]context.Context

	// ToolCallSpans maps tool call IDs to their display spans so that
	// CallBatch can end each span when its tool execution completes.
	ToolCallSpans map[string]trace.Span
}

// TextContent returns the concatenation of all text blocks.
func (r *LLMResponse) TextContent() string {
	var sb strings.Builder
	for _, b := range r.Content {
		if b.Kind == LLMContentText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// ToolCalls returns just the tool-call content blocks.
func (r *LLMResponse) ToolCalls() []*LLMContentBlock {
	var calls []*LLMContentBlock
	for _, b := range r.Content {
		if b.Kind == LLMContentToolCall {
			calls = append(calls, b)
		}
	}
	return calls
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

// LLMContentBlockKind identifies the kind of content in an LLMContentBlock.
type LLMContentBlockKind string

var LLMContentBlockKinds = dagql.NewEnum[LLMContentBlockKind]()

var (
	LLMContentText       = LLMContentBlockKinds.Register("TEXT", "Plain text content.")
	LLMContentThinking   = LLMContentBlockKinds.Register("THINKING", "Model thinking/reasoning content (e.g. Anthropic extended thinking).")
	LLMContentToolCall   = LLMContentBlockKinds.Register("TOOL_CALL", "A tool/function call from the model.")
	LLMContentToolResult = LLMContentBlockKinds.Register("TOOL_RESULT", "A tool/function result.")
	// Future: IMAGE, AUDIO, etc.
)

func (LLMContentBlockKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMContentBlockKind",
		NonNull:   true,
	}
}

func (t LLMContentBlockKind) TypeDescription() string {
	return "The kind of content in a message block."
}

func (t LLMContentBlockKind) Decoder() dagql.InputDecoder {
	return LLMContentBlockKinds
}

func (t LLMContentBlockKind) ToLiteral() call.Literal {
	return LLMContentBlockKinds.Literal(t)
}

// LLMContentBlock is a single piece of content within an LLMMessage.
// The Kind field determines which other fields are populated.
type LLMContentBlock struct {
	Kind LLMContentBlockKind `field:"true" json:"kind"`

	// Text content (Kind=TEXT, THINKING, or TOOL_RESULT)
	Text string `field:"true" json:"text,omitempty"`

	// Tool call fields (Kind=TOOL_CALL)
	CallID    string `field:"true" json:"call_id,omitempty"`
	ToolName  string `field:"true" json:"tool_name,omitempty"`
	Arguments JSON   `field:"true" json:"arguments,omitempty"`

	// Tool result fields (Kind=TOOL_RESULT)
	// CallID is reused from above.
	Errored bool `field:"true" json:"errored,omitempty"`

	// Provider-specific opaque data (e.g. Anthropic thinking signature).
	// Not exposed as a field — must be preserved in history but is
	// meaningless to users.
	Signature string `json:"signature,omitempty"`
}

func (*LLMContentBlock) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMContentBlock",
		NonNull:   true,
	}
}

// LLMContentBlockInput is the input object type for creating content blocks.
type LLMContentBlockInput struct {
	Kind      LLMContentBlockKind `doc:"The kind of content block."`
	Text      string              `doc:"Text content (for TEXT, THINKING, or TOOL_RESULT kinds)." default:""`
	CallID    string              `doc:"The unique ID of a tool call (for TOOL_CALL or TOOL_RESULT kinds)." default:""`
	ToolName  string              `doc:"The name of the tool to call (for TOOL_CALL kind)." default:""`
	Arguments JSON                `doc:"The arguments to pass to the tool (for TOOL_CALL kind)."`
	Errored   bool                `doc:"Whether the tool call resulted in an error (for TOOL_RESULT kind)." default:"false"`
	Signature string              `doc:"Provider-specific opaque data (e.g. Anthropic thinking signature)." default:""`
}

func (LLMContentBlockInput) TypeName() string {
	return "LLMContentBlockInput"
}

func (LLMContentBlockInput) TypeDescription() string {
	return "A content block within an LLM message."
}

// ToLLMContentBlock converts the input object to an LLMContentBlock.
func (in LLMContentBlockInput) ToLLMContentBlock() *LLMContentBlock {
	return &LLMContentBlock{
		Kind:      in.Kind,
		Text:      in.Text,
		CallID:    in.CallID,
		ToolName:  in.ToolName,
		Arguments: in.Arguments,
		Errored:   in.Errored,
		Signature: in.Signature,
	}
}

// LLMMessage represents a single message in the LLM conversation history.
// Content is a list of typed content blocks, supporting multi-modal and
// multi-part messages (e.g. thinking + text + tool calls in one turn).
type LLMMessage struct {
	Role    LLMMessageRole     `field:"true" json:"role"`
	Content []*LLMContentBlock `field:"true" json:"content"`

	// Token usage for this message (only set on assistant responses).
	TokenUsage LLMTokenUsage `json:"token_usage,omitzero"`
}

func (*LLMMessage) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMMessage",
		NonNull:   true,
	}
}

// TextContent returns the concatenation of all text blocks in this message.
func (m *LLMMessage) TextContent() string {
	var sb strings.Builder
	for _, b := range m.Content {
		if b.Kind == LLMContentText {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// ToolCalls returns the tool-call content blocks.
func (m *LLMMessage) ToolCalls() []*LLMContentBlock {
	var calls []*LLMContentBlock
	for _, b := range m.Content {
		if b.Kind == LLMContentToolCall {
			calls = append(calls, b)
		}
	}
	return calls
}

// IsToolResult returns true if this message is a tool result (has a TOOL_RESULT block).
func (m *LLMMessage) IsToolResult() bool {
	for _, b := range m.Content {
		if b.Kind == LLMContentToolResult {
			return true
		}
	}
	return false
}

// ToolResultContent returns the text from the first TOOL_RESULT block, if any.
func (m *LLMMessage) ToolResultContent() string {
	for _, b := range m.Content {
		if b.Kind == LLMContentToolResult {
			return b.Text
		}
	}
	return ""
}

// ToolResultCallID returns the call ID from the first TOOL_RESULT block, if any.
func (m *LLMMessage) ToolResultCallID() string {
	for _, b := range m.Content {
		if b.Kind == LLMContentToolResult {
			return b.CallID
		}
	}
	return ""
}

// ToolResultErrored returns whether the first TOOL_RESULT block is an error.
func (m *LLMMessage) ToolResultErrored() bool {
	for _, b := range m.Content {
		if b.Kind == LLMContentToolResult {
			return b.Errored
		}
	}
	return false
}

type LLMMessageRole string

var LLMMessageRoles = dagql.NewEnum[LLMMessageRole]()

var (
	LLMMessageRoleUser      = LLMMessageRoles.Register("USER", "A user prompt or tool response.")
	LLMMessageRoleAssistant = LLMMessageRoles.Register("ASSISTANT", "A reply from the model.")
	LLMMessageRoleSystem    = LLMMessageRoles.Register("SYSTEM", "A system prompt.")
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

// LLMToolCall is kept as a convenience type for the MCP layer and provider
// interfaces that work with tool calls as a flat list.
type LLMToolCall struct {
	CallID    string `json:"id"`
	Name      string `json:"name"`
	Arguments JSON   `json:"arguments"`
}

// ToContentBlock converts to the canonical content block representation.
func (tc *LLMToolCall) ToContentBlock() *LLMContentBlock {
	return &LLMContentBlock{
		Kind:      LLMContentToolCall,
		CallID:    tc.CallID,
		ToolName:  tc.Name,
		Arguments: tc.Arguments,
	}
}

func (*LLMToolCall) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMToolCall",
		NonNull:   true,
	}
}

const (
	OpenAI      LLMProvider = "openai"
	OpenAICodex LLMProvider = "openai-codex"
	Anthropic   LLMProvider = "anthropic"
	Google      LLMProvider = "google"
	Meta        LLMProvider = "meta"
	Mistral     LLMProvider = "mistral"
	DeepSeek    LLMProvider = "deepseek"
	Other       LLMProvider = "other"
)

// A LLM routing configuration
type LLMRouter struct {
	AnthropicAPIKey           string
	AnthropicBaseURL          string
	AnthropicModel            string
	AnthropicAuthToken        string // OAuth bearer token for Claude Code subscription
	AnthropicIsOAuth          bool   // Whether Anthropic auth uses OAuth
	AnthropicSubscriptionType string // "pro", "max", etc. (OAuth only)
	AnthropicThinkingMode     string // "adaptive", "enabled", "disabled"
	AnthropicThinkingBudget   int64  // Max thinking tokens (when mode == "enabled")

	OpenAIAPIKey           string
	OpenAIAzureVersion     string
	OpenAIBaseURL          string
	OpenAIModel            string
	OpenAIDisableStreaming bool

	// OpenAI Codex (ChatGPT subscription) fields
	OpenAICodexAuthToken    string // OAuth bearer token
	OpenAICodexModel        string
	OpenAICodexThinkingMode string // Reasoning effort: "none", "low", "medium", "high", "xhigh"

	GeminiAPIKey  string
	GeminiBaseURL string
	GeminiModel   string
}

func (r *LLMRouter) isAnthropicModel(model string) bool {
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "anthropic/")
}

func (r *LLMRouter) isCodexModel(model string) bool {
	return strings.Contains(model, "codex") || strings.HasPrefix(model, "openai-codex/")
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
		BaseURL:          r.AnthropicBaseURL,
		Key:              r.AnthropicAPIKey,
		Provider:         Anthropic,
		AuthToken:        r.AnthropicAuthToken,
		IsOAuth:          r.AnthropicIsOAuth,
		SubscriptionType: r.AnthropicSubscriptionType,
		ThinkingMode:     r.AnthropicThinkingMode,
		ThinkingBudget:   r.AnthropicThinkingBudget,
	}
	endpoint.Client = newAnthropicClient(endpoint)

	return endpoint
}

func (r *LLMRouter) routeCodexModel() *LLMEndpoint {
	endpoint := &LLMEndpoint{
		BaseURL:      "https://chatgpt.com/backend-api",
		AuthToken:    r.OpenAICodexAuthToken,
		IsOAuth:      true,
		Provider:     OpenAICodex,
		ThinkingMode: r.OpenAICodexThinkingMode,
	}
	endpoint.Client = newOpenAICodexClient(endpoint)
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
	for _, model := range []string{r.OpenAICodexModel, r.OpenAIModel, r.AnthropicModel, r.GeminiModel} {
		if model != "" {
			return model
		}
	}
	if r.OpenAICodexAuthToken != "" {
		return modelDefaultCodex
	}
	if r.OpenAIAPIKey != "" {
		return modelDefaultOpenAI
	}
	if r.AnthropicAPIKey != "" || r.AnthropicAuthToken != "" {
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
	case r.isCodexModel(model):
		endpoint = r.routeCodexModel()
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

// LoadFromConfig populates router from config file (base layer).
// Only sets fields that are currently empty, allowing env vars to take priority.
func (r *LLMRouter) LoadFromConfig(cfg *llmconfig.Config) {
	if cfg == nil {
		return
	}

	for name, provider := range cfg.LLM.Providers {
		if !provider.Enabled {
			continue
		}

		switch name {
		case "openrouter":
			// OpenRouter uses OpenAI-compatible API
			if r.OpenAIAPIKey == "" {
				r.OpenAIAPIKey = provider.APIKey
			}
			if r.OpenAIBaseURL == "" {
				r.OpenAIBaseURL = "https://openrouter.ai/api/v1"
				if provider.BaseURL != "" {
					r.OpenAIBaseURL = provider.BaseURL
				}
			}
		case "anthropic":
			if provider.IsOAuth() {
				// OAuth takes precedence over API key
				if r.AnthropicAuthToken == "" {
					r.AnthropicAuthToken = provider.AuthToken
					r.AnthropicIsOAuth = true
					r.AnthropicSubscriptionType = provider.SubscriptionType
				}
			} else if r.AnthropicAPIKey == "" {
				r.AnthropicAPIKey = provider.APIKey
			}
			if r.AnthropicBaseURL == "" && provider.BaseURL != "" {
				r.AnthropicBaseURL = provider.BaseURL
			}
			if provider.ThinkingMode != "" {
				r.AnthropicThinkingMode = provider.ThinkingMode
				r.AnthropicThinkingBudget = provider.ThinkingBudget
			}
		case "openai":
			if r.OpenAIAPIKey == "" {
				r.OpenAIAPIKey = provider.APIKey
			}
			if r.OpenAIBaseURL == "" && provider.BaseURL != "" {
				r.OpenAIBaseURL = provider.BaseURL
			}
		case "openai-codex":
			if provider.IsOAuth() && r.OpenAICodexAuthToken == "" {
				r.OpenAICodexAuthToken = provider.AuthToken
			}
			if provider.ThinkingMode != "" {
				r.OpenAICodexThinkingMode = provider.ThinkingMode
			}
		case "google":
			if r.GeminiAPIKey == "" {
				r.GeminiAPIKey = provider.APIKey
			}
		}
	}

	// Set default model for the default provider if not already set
	if cfg.LLM.DefaultModel != "" {
		switch cfg.LLM.DefaultProvider {
		case "openrouter", "openai":
			if r.OpenAIModel == "" {
				r.OpenAIModel = cfg.LLM.DefaultModel
			}
		case "anthropic":
			if r.AnthropicModel == "" {
				r.AnthropicModel = cfg.LLM.DefaultModel
			}
		case "openai-codex":
			if r.OpenAICodexModel == "" {
				r.OpenAICodexModel = cfg.LLM.DefaultModel
			}
		case "google":
			if r.GeminiModel == "" {
				r.GeminiModel = cfg.LLM.DefaultModel
			}
		}
	}
}

// IsEmpty returns true if no LLM configuration is available.
func (r *LLMRouter) IsEmpty() bool {
	return r.OpenAIAPIKey == "" &&
		r.AnthropicAPIKey == "" &&
		r.AnthropicAuthToken == "" &&
		r.GeminiAPIKey == ""
}

// ErrNoLLMConfig is returned when no LLM configuration is found.
type ErrNoLLMConfig struct{}

func (e *ErrNoLLMConfig) Error() string {
	return `No LLM configuration found.

To get started, run:
    dagger llm setup

Or set environment variables:
    export ANTHROPIC_API_KEY=sk-ant-...
    export OPENAI_API_KEY=sk-...
    export GEMINI_API_KEY=AIza...

For unified access to all models with a single key:
    https://openrouter.ai/keys
`
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
	defer telemetry.EndWithCause(span, &rerr)

	// Priority order:
	// 1. Config file (~/.config/dagger/config.toml) - base layer
	// 2. .env file - middle layer (legacy support)
	// 3. Environment variables - top layer (overrides everything)

	// First: Try loading from config file via the client's filesystem.
	// The config path is resolved client-side (via xdg.ConfigHome) and passed
	// in ClientMetadata so it works cross-platform (Unix, macOS, Windows).
	configPath := ""
	if clientMD, err := engine.ClientMetadataFromContext(ctx); err == nil && clientMD.ConfigPath != "" {
		configPath = clientMD.ConfigPath
	}
	if configPath != "" {
		if configData, err := loadSecret(ctx, "file://"+configPath); err == nil && configData != "" {
			var cfg llmconfig.Config
			if err := toml.Unmarshal([]byte(configData), &cfg); err == nil {
				if cfg.LLM.Providers == nil {
					cfg.LLM.Providers = make(map[string]llmconfig.Provider)
				}
				// Resolve API keys that may be secret references (e.g. op://vault/item/field)
				for name, provider := range cfg.LLM.Providers {
					if provider.APIKey != "" {
						if resolved, err := loadSecret(ctx, provider.APIKey); err == nil {
							provider.APIKey = resolved
							cfg.LLM.Providers[name] = provider
						}
					}
				}
				router.LoadFromConfig(&cfg)
			}
		}
	}

	// Second: Load .env from current directory, if it exists
	env := make(map[string]string)
	if envFile, err := loadSecret(ctx, "file://.env"); err == nil {
		if e, err := godotenv.Unmarshal(envFile); err == nil {
			env = e
		}
	}

	// Third: Load environment variables (highest priority, overrides everything)
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
	if err != nil {
		return nil, err
	}

	return router, nil
}

func (q *Query) NewLLM(ctx context.Context, model string) (*LLM, error) {
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

func (llm *LLM) HasMissingOutputs() bool {
	for _, out := range llm.Env().Self().outputsByName {
		if out.Value == nil {
			return false
		}
	}
	return true
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
	if prompt == "" {
		return llm
	}
	llm = llm.Clone()
	llm.Messages = append(llm.Messages, &LLMMessage{
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{{
			Kind: LLMContentText,
			Text: prompt,
		}},
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

// WithoutMessageHistory removes all messages, leaving only the system prompts
func (llm *LLM) WithoutMessageHistory() *LLM {
	llm = llm.Clone()
	llm.Messages = slices.DeleteFunc(llm.Messages, func(msg *LLMMessage) bool {
		return msg.Role != "system"
	})
	return llm
}

// WithoutSystemPrompts removes all system prompts from the history, leaving
// only the default system prompt
func (llm *LLM) WithoutSystemPrompts() *LLM {
	llm = llm.Clone()
	llm.Messages = slices.DeleteFunc(llm.Messages, func(msg *LLMMessage) bool {
		return msg.Role == "system"
	})
	return llm
}

// Append a system prompt message to the history
func (llm *LLM) WithSystemPrompt(prompt string) *LLM {
	if prompt == "" {
		return llm
	}
	llm = llm.Clone()
	llm.Messages = append(llm.Messages, &LLMMessage{
		Role: LLMMessageRoleSystem,
		Content: []*LLMContentBlock{{
			Kind: LLMContentText,
			Text: prompt,
		}},
	})
	return llm
}

// WithResponse appends an assistant response to the message history.
// The content blocks come directly from the LLMResponse.
func (llm *LLM) WithResponse(blocks []*LLMContentBlock, tokenUsage LLMTokenUsage) *LLM {
	llm = llm.Clone()
	llm.Messages = append(llm.Messages, &LLMMessage{
		Role:       LLMMessageRoleAssistant,
		Content:    blocks,
		TokenUsage: tokenUsage,
	})
	return llm
}

// Append a tool call to the last assistant message in the history
func (llm *LLM) WithToolCall(callID, tool string, arguments JSON) *LLM {
	llm = llm.Clone()
	// Find the last assistant message and append the tool call block to it
	for i := len(llm.Messages) - 1; i >= 0; i-- {
		if llm.Messages[i].Role == LLMMessageRoleAssistant {
			llm.Messages[i].Content = append(llm.Messages[i].Content, &LLMContentBlock{
				Kind:      LLMContentToolCall,
				CallID:    callID,
				ToolName:  tool,
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
		Role: LLMMessageRoleUser,
		Content: []*LLMContentBlock{{
			Kind:    LLMContentToolResult,
			Text:    content,
			CallID:  callID,
			Errored: errored,
		}},
	})
	return llm
}

// Append a tool response (user) message to the history
func (llm *LLM) WithObject(objectID string, id ID) *LLM {
	llm = llm.Clone()
	llm.mcp = llm.mcp.WithObject(objectID, id)
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
func (llm *LLM) LastReply() (string, bool) {
	var reply string = "(no reply)"
	var foundReply bool
	for _, msg := range llm.Messages {
		if msg.Role != LLMMessageRoleAssistant {
			continue
		}
		txt := msg.TextContent()
		if len(txt) == 0 {
			continue
		}
		foundReply = true
		reply = txt
	}
	return reply, foundReply
}

func (llm *LLM) messagesWithSystemPrompt() []*LLMMessage {
	var systemPrompt string
	if !llm.disableDefaultSystemPrompt {
		systemPrompt = llm.mcp.DefaultSystemPrompt()
	}
	if systemPrompt != "" {
		return append([]*LLMMessage{{
			Role: LLMMessageRoleSystem,
			Content: []*LLMContentBlock{{
				Kind: LLMContentText,
				Text: systemPrompt,
			}},
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
	if err := llm.allowed(ctx); err != nil {
		return inst, err
	}
	return llm.step(ctx, inst)
}

func (llm *LLM) step(ctx context.Context, inst dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
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
		if msg.Role == LLMMessageRoleAssistant || msg.IsToolResult() {
			// only display messages appended since the last response
			break
		}
		newMessages = append(newMessages, msg)
	}
	slices.Reverse(newMessages)

	// Compute the LLM call digest for prompt/response span metadata.
	// inst.ID() is the LLM state entering step() (typically ends in
	// withPrompt). Its digest lets the TUI identify and branch from
	// this point in the conversation.
	llmCallDigest := inst.ID().Digest().String()

	for _, msg := range newMessages {
		emitMessageSpan(ctx, msg, llmCallDigest)
	}

	var res *LLMResponse

	ep, err := llm.Endpoint(ctx)
	if err != nil {
		return inst, err
	}
	client := ep.Client
	err = backoff.Retry(func() error {
		var sendErr error
		res, sendErr = client.SendQuery(ctx, messagesToSend, tools)
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
	{
		// Build content block input objects for the withResponse selector.
		contentInputs := make(dagql.ArrayInput[dagql.InputObject[LLMContentBlockInput]], len(res.Content))
		for i, block := range res.Content {
			contentInputs[i] = dagql.InputObject[LLMContentBlockInput]{
				Value: LLMContentBlockInput{
					Kind:      block.Kind,
					Text:      block.Text,
					CallID:    block.CallID,
					ToolName:  block.ToolName,
					Arguments: block.Arguments,
					Errored:   block.Errored,
					Signature: block.Signature,
				},
			}
		}
		args := []dagql.NamedInput{
			{
				Name:  "content",
				Value: contentInputs,
			},
		}
		if res.TokenUsage.InputTokens != 0 {
			args = append(args, dagql.NamedInput{
				Name:  "inputTokens",
				Value: dagql.NewInt(res.TokenUsage.InputTokens),
			})
		}
		if res.TokenUsage.OutputTokens != 0 {
			args = append(args, dagql.NamedInput{
				Name:  "outputTokens",
				Value: dagql.NewInt(res.TokenUsage.OutputTokens),
			})
		}
		if res.TokenUsage.CachedTokenReads != 0 {
			args = append(args, dagql.NamedInput{
				Name:  "cachedTokenReads",
				Value: dagql.NewInt(res.TokenUsage.CachedTokenReads),
			})
		}
		if res.TokenUsage.CachedTokenWrites != 0 {
			args = append(args, dagql.NamedInput{
				Name:  "cachedTokenWrites",
				Value: dagql.NewInt(res.TokenUsage.CachedTokenWrites),
			})
		}
		if res.TokenUsage.TotalTokens != 0 {
			args = append(args, dagql.NamedInput{
				Name:  "totalTokens",
				Value: dagql.NewInt(res.TokenUsage.TotalTokens),
			})
		}
		sels = append(sels, dagql.Selector{
			Field: "withResponse",
			Args:  args,
		})
	}
	// Extract tool calls from response content blocks for the MCP layer.
	var toolCalls []*LLMToolCall
	for _, block := range res.Content {
		if block.Kind == LLMContentToolCall {
			toolCalls = append(toolCalls, &LLMToolCall{
				CallID:    block.CallID,
				Name:      block.ToolName,
				Arguments: block.Arguments,
			})
		}
	}
	beforeObjs := maps.Clone(llm.mcp.objsByID)
	for _, msg := range llm.mcp.CallBatch(ctx, tools, toolCalls, res.ToolCallCtxs, res.ToolCallSpans) {
		sels = append(sels, dagql.Selector{
			Field: "withToolResponse",
			Args: []dagql.NamedInput{
				{
					Name:  "call",
					Value: dagql.NewString(msg.ToolResultCallID()),
				},
				{
					Name:  "content",
					Value: dagql.NewString(msg.ToolResultContent()),
				},
				{
					Name:  "errored",
					Value: dagql.NewBoolean(msg.ToolResultErrored()),
				},
			},
		})
	}
	var newObjs []string
	for obj := range llm.mcp.objsByID {
		if _, exists := beforeObjs[obj]; exists {
			continue
		}
		newObjs = append(newObjs, obj)
	}
	sort.Strings(newObjs)
	for _, objID := range newObjs {
		id := llm.mcp.objsByID[objID]
		sels = append(sels, dagql.Selector{
			Field: "__withObject",
			Args: []dagql.NamedInput{
				{
					Name:  "tag",
					Value: dagql.NewString(objID),
				},
				{
					Name:  "object",
					Value: ID{Inner: id},
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

	// Tool call spans have already been ended by CallBatch. Collect
	// the remaining (non-tool-call) display spans that still need to
	// be ended by us.
	endedSpans := make(map[trace.Span]bool, len(res.ToolCallSpans))
	for _, s := range res.ToolCallSpans {
		endedSpans[s] = true
	}
	var remainingSpans []trace.Span
	for _, s := range res.DisplaySpans {
		if !endedSpans[s] {
			remainingSpans = append(remainingSpans, s)
		}
	}

	var stepped dagql.ObjectResult[*LLM]
	err = srv.Select(ctx, inst, &stepped, sels...)
	if err != nil {
		for _, s := range remainingSpans {
			s.End()
		}
		return inst, err
	}

	if len(remainingSpans) > 0 {
		responseDigest := stepped.ID().Digest().String()
		for _, s := range remainingSpans {
			s.SetAttributes(attribute.String(LLMCallDigestAttr, responseDigest))
			s.End()
		}
	}

	return stepped, nil
}

// Loop sends the context to the LLM endpoint, processes replies and tool calls; continues in a loop
// Synchronize LLM state:
// 1. Send context to LLM endpoint
// 2. Process replies and tool calls
// 3. Continue in a loop until no tool calls, or caps are reached
func (llm *LLM) Loop(ctx context.Context, inst dagql.ObjectResult[*LLM], maxAPICalls int) (dagql.ObjectResult[*LLM], error) {
	if err := llm.allowed(ctx); err != nil {
		return inst, err
	}

	var apiCalls int
	for {
		llm := inst.Self()

		if !llm.HasPrompt() {
			if llm.HasMissingOutputs() {
				// There's no prompt, and yet there are outputs unfulfilled. This means
				// future calls to Env.Output may fail, so we should interject to help
				// the LLM along.

				if newLLM, interjected, interjectErr := llm.Interject(ctx, inst); interjectErr != nil {
					if ctx.Err() != nil {
						return inst, nil
					}
					return inst, interjectErr
				} else if interjected {
					inst = newLLM
					// interjected - continue
					continue
				} else {
					// no interjection - user gave up?
					break
				}
			}

			// nothing to do - either never prompted, or naturally reached the end of
			// the loop (e.g. LLM reply with no additional tool calls)
			return inst, nil
		}

		if maxAPICalls > 0 && apiCalls >= maxAPICalls {
			return inst, fmt.Errorf("reached API call limit: %d", apiCalls)
		}

		apiCalls++

		var err error
		inst, err = inst.Self().Step(ctx, inst)
		if err != nil {
			if ctx.Err() != nil {
				// Context was cancelled (user interrupt). Return the
				// last successfully recorded state so that the prompt
				// and any prior progress are preserved in history.
				return inst, nil
			}
			// Handle persistent error after all retries failed.
			return inst, err
		}
	}

	return inst, nil
}

func (llm *LLM) Interject(ctx context.Context, self dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], bool, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return self, false, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return self, false, err
	}
	if !bk.Opts.Interactive {
		return self, false, nil
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return self, false, err
	}
	interjectAttrs := []attribute.KeyValue{
		attribute.String(telemetry.UIActorEmojiAttr, "🧑"),
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageSent),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleUser),
		attribute.String(LLMCallDigestAttr, self.ID().Digest().String()),
	}
	ctx, span := Tracer(ctx).Start(ctx, "LLM prompt", telemetry.Reveal(), trace.WithAttributes(
		interjectAttrs...,
	))
	defer span.End()
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))
	defer stdio.Close()
	lastAssistantMessage, foundReply := llm.LastReply()
	if !foundReply {
		return self, false, fmt.Errorf("no message from assistant")
	}
	msg, err := bk.PromptHumanHelp(ctx, "LLM needs help!", fmt.Sprintf("The LLM was unable to complete its task and needs a prompt to continue. Here is its last message:\n%s", mdQuote(lastAssistantMessage)))
	if err != nil {
		return self, false, err
	}
	if msg == "" {
		return self, false, nil
	}
	fmt.Fprint(stdio.Stdout, msg)

	var inst dagql.ObjectResult[*LLM]
	if err := srv.Select(ctx, self, &inst, dagql.Selector{
		Field: "withPrompt",
		Args: []dagql.NamedInput{
			{
				Name:  "prompt",
				Value: dagql.NewString(msg),
			},
		},
	}); err != nil {
		return self, false, err
	}
	return inst, true, nil
}

func mdQuote(msg string) string {
	lines := strings.Split(msg, "\n")
	for i, line := range lines {
		lines[i] = fmt.Sprintf("> %s", line)
	}
	return strings.Join(lines, "\n")
}

func (llm *LLM) HasPrompt() bool {
	return len(llm.Messages) > 0 && llm.Messages[len(llm.Messages)-1].Role == LLMMessageRoleUser
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

// emitMessageSpan creates a telemetry span for a single LLM message.
// This is used both during live step() execution and during replay.
// callDigest is the DAG digest enabling TUI branching from that point.
func emitMessageSpan(ctx context.Context, msg *LLMMessage, callDigest string) {
	switch msg.Role {
	case LLMMessageRoleUser, LLMMessageRoleSystem:
		emitUserMessageSpan(ctx, msg, callDigest)
	case LLMMessageRoleAssistant:
		emitAssistantMessageSpan(ctx, msg, callDigest)
	}
}

func emitUserMessageSpan(ctx context.Context, msg *LLMMessage, callDigest string) {
	var emoji string
	switch msg.Role {
	case LLMMessageRoleUser:
		emoji = "🧑"
	case LLMMessageRoleSystem:
		emoji = "⚙️"
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.UIActorEmojiAttr, emoji),
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageSent),
		attribute.String(telemetry.LLMRoleAttr, msg.Role.String()),
		attribute.Bool(telemetry.UIInternalAttr, msg.Role == LLMMessageRoleSystem),
	}
	if callDigest != "" {
		attrs = append(attrs, attribute.String(LLMCallDigestAttr, callDigest))
	}
	ctx, span := Tracer(ctx).Start(ctx, "LLM prompt",
		telemetry.Reveal(),
		trace.WithAttributes(attrs...))
	defer span.End()
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))
	defer stdio.Close()
	fmt.Fprint(stdio.Stdout, msg.TextContent())
}

func emitAssistantMessageSpan(ctx context.Context, msg *LLMMessage, callDigest string) {
	// Each content block gets its own span, matching the provider streaming
	// behavior: thinking, text (LLM response), and tool calls each appear
	// separately. Contiguous runs of the same non-tool-call type are grouped.
	type spanGroup struct {
		kind   LLMContentBlockKind // LLMContentThinking, LLMContentText, or LLMContentToolCall
		blocks []*LLMContentBlock
	}
	var groups []spanGroup
	for _, block := range msg.Content {
		// Tool calls always get their own span (one per call).
		if block.Kind == LLMContentToolCall {
			groups = append(groups, spanGroup{kind: block.Kind, blocks: []*LLMContentBlock{block}})
			continue
		}
		// Group contiguous thinking or text blocks together.
		if len(groups) > 0 && groups[len(groups)-1].kind == block.Kind {
			groups[len(groups)-1].blocks = append(groups[len(groups)-1].blocks, block)
		} else {
			groups = append(groups, spanGroup{kind: block.Kind, blocks: []*LLMContentBlock{block}})
		}
	}

	for _, g := range groups {
		func() {
			var name string
			var extraAttrs []attribute.KeyValue
			var contentType string
			switch g.kind {
			case LLMContentThinking:
				name = "thinking"
				contentType = "text/markdown"
				extraAttrs = append(extraAttrs,
					attribute.String(telemetry.UIActorEmojiAttr, "💭"),
					attribute.Bool("llm.thinking", true),
				)
			case LLMContentToolCall:
				block := g.blocks[0]
				name = block.ToolName
				contentType = "application/json"
				extraAttrs = append(extraAttrs,
					attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
					attribute.String(telemetry.LLMToolAttr, block.ToolName),
				)
			default:
				name = "LLM response"
				contentType = "text/markdown"
				extraAttrs = append(extraAttrs,
					attribute.String(telemetry.UIActorEmojiAttr, "🤖"),
				)
			}
			attrs := []attribute.KeyValue{
				attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
				attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
			}
			attrs = append(attrs, extraAttrs...)
			if callDigest != "" {
				attrs = append(attrs, attribute.String(LLMCallDigestAttr, callDigest))
			}
			ctx, span := Tracer(ctx).Start(ctx, name,
				telemetry.Reveal(),
				trace.WithAttributes(attrs...))
			defer span.End()
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
				log.String(telemetry.ContentTypeAttr, contentType))
			defer stdio.Close()
			for _, block := range g.blocks {
				switch block.Kind {
				case LLMContentText, LLMContentThinking:
					fmt.Fprint(stdio.Stdout, block.Text)
				case LLMContentToolCall:
					fmt.Fprint(stdio.Stdout, string(block.Arguments))
				}
			}
		}()
	}
}

// Replay re-emits telemetry spans for all messages in the conversation history.
// This allows the TUI to display the conversation after loading a saved session.
func (llm *LLM) Replay(ctx context.Context) {
	for _, msg := range llm.Messages {
		// We don't have per-message call digests for replay, so pass empty.
		// The TUI will still display the messages, just without branch support.
		emitMessageSpan(ctx, msg, "")
	}
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
		switch msg.Role {
		case LLMMessageRoleUser:
			for _, block := range msg.Content {
				switch block.Kind {
				case LLMContentToolResult:
					item := "🛠️ 💬 "
					if block.Errored {
						item += "ERROR: "
					}
					item += squash(block.Text)
					history = append(history, item)
				case LLMContentText:
					history = append(history, "🧑 💬 "+squash(block.Text))
				}
			}
		case LLMMessageRoleAssistant:
			for _, block := range msg.Content {
				switch block.Kind {
				case LLMContentThinking:
					history = append(history, "💭 "+squash(block.Text))
				case LLMContentText:
					if len(block.Text) > 0 {
						history = append(history, "🤖 💬 "+squash(block.Text))
					}
				case LLMContentToolCall:
					item := fmt.Sprintf("🤖 🛠️ %s %s", block.ToolName, block.Arguments)
					history = append(history, item)
				}
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
	llm.mcp.setEnv(env)
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
