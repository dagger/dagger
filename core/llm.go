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

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/cenkalti/backoff/v4"
	telemetry "github.com/dagger/otel-go"
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
	"github.com/dagger/dagger/engine/telemetryattrs"
)

func init() {
	strcase.ConfigureAcronym("LLM", "LLM")
}

const (
	modelDefaultAnthropic = anthropic.ModelClaudeSonnet4_5
	modelDefaultGoogle    = "gemini-2.5-flash"
	modelDefaultOpenAI    = "gpt-4.1"
	modelDefaultCodex     = "gpt-5.5"
	modelDefaultMeta      = "llama-3.2"
	modelDefaultMistral   = "mistral-7b-instruct"
)

// codexModelPrefix pins a model to the Codex (ChatGPT subscription) backend.
// Current Codex models (gpt-5.5, gpt-5.4, …) no longer carry "codex" in their
// IDs, so the prefix — not the name — is what routes them to the Codex client.
// Route strips it back off so the model still displays and is sent to the API
// under its bare name.
const codexModelPrefix = "openai-codex/"

// normalizeCodexModel ensures a model configured for the Codex slot routes to
// the Codex client regardless of its name, by prefixing it with
// codexModelPrefix. Models that already route to Codex (a "codex"-named model
// or an already-prefixed one) are left untouched.
func normalizeCodexModel(model string) string {
	if model == "" || strings.Contains(model, "codex") || strings.HasPrefix(model, codexModelPrefix) {
		return model
	}
	return codexModelPrefix + model
}

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
	// The full message history, exposed over the API as first-class content
	// blocks so that conversations can be queried and branched.
	Messages []*LLMMessage `field:"true" doc:"The full message history."`

	// The environment accessible to the LLM, exposed over MCP
	mcp *MCP

	model string

	endpoint    *LLMEndpoint
	endpointMtx *sync.Mutex

	// Whether to disable the default system prompt
	disableDefaultSystemPrompt bool

	// maxTokens limits the number of output tokens the model may generate
	// per API call. Zero means use provider defaults.
	maxTokens int
}

type LLMEndpoint struct {
	Model    string
	BaseURL  string
	Key      string
	Provider LLMProvider
	Client   LLMClient

	// AuthToken and IsOAuth carry subscription OAuth credentials (e.g. Claude
	// Code). When IsOAuth is set, the provider client authenticates with
	// AuthToken as a bearer token instead of Key.
	AuthToken string
	IsOAuth   bool

	// ThinkingMode carries the reasoning effort (e.g. "low"/"medium"/"high",
	// or "adaptive") for providers that support extended thinking / reasoning
	// (e.g. Anthropic, Google, Codex). Empty means thinking is disabled.
	ThinkingMode string

	// ThinkingBudget is the maximum number of thinking tokens the model may
	// spend per turn, for providers that accept an explicit budget (Anthropic,
	// Google). Zero means use a provider-specific default.
	ThinkingBudget int64

	// tunnel holds a running container-to-host tunnel for local endpoints,
	// forwarding the endpoint's traffic through the client's session. Nil for
	// non-local providers.
	tunnel *localTunnel
}

type LLMProvider string

// LLMClient interface defines the methods that each provider must implement
type LLMClient interface {
	SendQuery(ctx context.Context, history []*LLMMessage, tools []LLMTool, opts *LLMCallOpts) (*LLMResponse, error)
	IsRetryable(err error) bool
}

// LLMCallOpts carries per-call options from the LLM state to the provider.
type LLMCallOpts struct {
	// MaxTokens limits the number of output/completion tokens. Zero means
	// the provider should use its own default.
	MaxTokens int

	// CallDigest is this turn's LLM state digest, set on the live display spans
	// the provider creates so the TUI can branch the conversation from them.
	CallDigest string
}

// LLMResponse is the internal result returned by a provider's SendQuery.
// It carries content blocks and token usage but is not exposed in the API;
// the evaluation loop converts it into LLMMessage history entries.
type LLMResponse struct {
	Content    []*LLMContentBlock
	TokenUsage LLMTokenUsage

	// DisplaySpans are the OTel spans a provider created to stream this turn's
	// content live (thinking, text, tool-call arguments), in close order. The
	// loop ends any that CallBatch didn't already end.
	DisplaySpans []trace.Span

	// ToolCallDisplays maps a tool call's ID to the display span its arguments
	// streamed into, so CallBatch can parent the tool's execution beneath it and
	// end the span once the tool returns.
	ToolCallDisplays map[string]toolCallDisplay
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
	// InputTokens is uncached input/prompt tokens. Cached input is accounted for
	// separately in CachedTokenReads/CachedTokenWrites so the buckets are
	// additive for cost and context accounting.
	InputTokens       int64 `field:"true" json:"input_tokens"`
	OutputTokens      int64 `field:"true" json:"output_tokens"`
	CachedTokenReads  int64 `field:"true" json:"cached_token_reads"`
	CachedTokenWrites int64 `field:"true" json:"cached_token_writes"`
	// TotalTokens is the provider-reported total tokens for a single call when
	// available, otherwise the sum of the additive buckets above.
	TotalTokens int64 `field:"true" json:"total_tokens"`
}

func (usage LLMTokenUsage) hasTokens() bool {
	return usage.InputTokens != 0 ||
		usage.OutputTokens != 0 ||
		usage.CachedTokenReads != 0 ||
		usage.CachedTokenWrites != 0 ||
		usage.TotalTokens != 0
}

// contextTokens returns the number of tokens represented by this usage record
// for context-window purposes. Providers should fill TotalTokens as the sum of
// uncached input, output, cache reads, and cache writes, but using the max keeps
// native provider totals that include extra categories (e.g. reasoning/tool-use
// accounting) from being truncated.
func (usage LLMTokenUsage) contextTokens() int64 {
	components := usage.InputTokens + usage.OutputTokens + usage.CachedTokenReads + usage.CachedTokenWrites
	return max(usage.TotalTokens, components)
}

func uncachedInputTokens(promptTokens, cachedTokens int64) int64 {
	if cachedTokens <= 0 {
		return promptTokens
	}
	if promptTokens >= cachedTokens {
		return promptTokens - cachedTokens
	}
	return promptTokens
}

var _ dagql.PersistedObject = (*LLMTokenUsage)(nil)
var _ dagql.PersistedObjectDecoder = (*LLMTokenUsage)(nil)

func (*LLMTokenUsage) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMTokenUsage",
		NonNull:   true,
	}
}

func (usage *LLMTokenUsage) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if usage == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted LLM token usage: nil LLM token usage")
	}
	return encodePersistedObjectPayload(usage)
}

func (*LLMTokenUsage) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var usage LLMTokenUsage
	if err := json.Unmarshal(payload, &usage); err != nil {
		return nil, fmt.Errorf("decode persisted LLM token usage payload: %w", err)
	}
	return &usage, nil
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

// estimateTokens returns a conservative token estimate for a message when the
// provider has not reported exact usage yet. It mirrors Pi's chars/4 fallback
// and is only used for messages after the last provider usage record (usually
// tool results queued for the next call) or before the first model call.
func (m *LLMMessage) estimateTokens() int64 {
	var chars int
	for _, b := range m.Content {
		chars += len(b.Text)
		chars += len(b.CallID)
		chars += len(b.ToolName)
		chars += len(b.Arguments)
	}
	if chars == 0 {
		return 0
	}
	return int64((chars + 3) / 4)
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

func (mode LLMMessageRole) String() string {
	return string(mode)
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
	Local       LLMProvider = "local"
	Other       LLMProvider = "other"
)

// A LLM routing configuration
type LLMRouter struct {
	AnthropicAPIKey         string
	AnthropicAuthToken      string
	AnthropicIsOAuth        bool
	AnthropicBaseURL        string
	AnthropicModel          string
	AnthropicThinkingMode   string
	AnthropicThinkingBudget int64

	OpenAIAPIKey           string
	OpenAIAzureVersion     string
	OpenAIBaseURL          string
	OpenAIModel            string
	OpenAIDisableStreaming bool

	// OpenAI Codex uses the Responses API against the ChatGPT backend with a
	// ChatGPT subscription OAuth token.
	OpenAICodexAuthToken    string
	OpenAICodexModel        string
	OpenAICodexThinkingMode string

	GeminiAPIKey         string
	GeminiBaseURL        string
	GeminiModel          string
	GeminiThinkingMode   string
	GeminiThinkingBudget int64

	// Local is a self-hosted, OpenAI- or Anthropic-compatible endpoint (e.g.
	// Ollama, LM Studio, vLLM) reachable from the client's host. Its traffic is
	// tunneled to the engine through the client's session, since the engine may
	// not be able to reach the endpoint directly. APICompat selects the wire
	// protocol ("openai" or "anthropic"); APIKey is optional.
	LocalBaseURL   string
	LocalModel     string
	LocalAPICompat string
	LocalAPIKey    string
}

func (r *LLMRouter) isAnthropicModel(model string) bool {
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "anthropic/")
}

func (r *LLMRouter) isOpenAIModel(model string) bool {
	return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "openai/")
}

func (r *LLMRouter) isCodexModel(model string) bool {
	return strings.Contains(model, "codex") || strings.HasPrefix(model, "openai-codex/")
}

func (r *LLMRouter) isGoogleModel(model string) bool {
	return strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "google/")
}

func (r *LLMRouter) isMistralModel(model string) bool {
	return strings.HasPrefix(model, "mistral-") || strings.HasPrefix(model, "mistral/")
}

// isLocalModel reports whether model is served by the configured local
// endpoint. Unlike the other providers, local models have no naming convention
// to key on, so we match the configured model name exactly.
func (r *LLMRouter) isLocalModel(model string) bool {
	return r.LocalBaseURL != "" && r.LocalAPICompat != "" && r.LocalModel == model
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
		BaseURL:        r.AnthropicBaseURL,
		Key:            r.AnthropicAPIKey,
		Provider:       Anthropic,
		AuthToken:      r.AnthropicAuthToken,
		IsOAuth:        r.AnthropicIsOAuth,
		ThinkingMode:   r.AnthropicThinkingMode,
		ThinkingBudget: r.AnthropicThinkingBudget,
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

func (r *LLMRouter) routeCodexModel() *LLMEndpoint {
	endpoint := &LLMEndpoint{
		// The Codex client appends "/codex" to reach the Responses API.
		BaseURL:      "https://chatgpt.com/backend-api",
		Provider:     OpenAICodex,
		AuthToken:    r.OpenAICodexAuthToken,
		IsOAuth:      true,
		ThinkingMode: r.OpenAICodexThinkingMode,
	}
	endpoint.Client = newOpenAICodexClient(endpoint)

	return endpoint
}

func (r *LLMRouter) routeGoogleModel() (*LLMEndpoint, error) {
	endpoint := &LLMEndpoint{
		BaseURL:        r.GeminiBaseURL,
		Key:            r.GeminiAPIKey,
		Provider:       Google,
		ThinkingMode:   r.GeminiThinkingMode,
		ThinkingBudget: r.GeminiThinkingBudget,
	}
	client, err := newGenaiClient(endpoint)
	if err != nil {
		return nil, err
	}
	endpoint.Client = client

	return endpoint, nil
}

func (r *LLMRouter) routeLocalModel() (*LLMEndpoint, error) {
	endpoint := &LLMEndpoint{
		BaseURL:  r.LocalBaseURL,
		Key:      r.LocalAPIKey,
		Provider: Local,
	}
	switch r.LocalAPICompat {
	case "openai":
		endpoint.Client = newOpenAIClient(endpoint, "", false)
	case "anthropic":
		endpoint.Client = newAnthropicClient(endpoint)
	default:
		return nil, fmt.Errorf("unsupported local API compatibility mode: %q (must be %q or %q)", r.LocalAPICompat, "openai", "anthropic")
	}
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
	if r.OpenAIModel != "" {
		return r.OpenAIModel
	}
	if r.OpenAICodexModel != "" {
		// The codex slot is unambiguous, so pin it to Codex even if the
		// configured model (e.g. gpt-5.5) shares OpenAI's naming.
		return normalizeCodexModel(r.OpenAICodexModel)
	}
	if r.AnthropicModel != "" {
		return r.AnthropicModel
	}
	if r.GeminiModel != "" {
		return r.GeminiModel
	}
	if r.LocalModel != "" {
		return r.LocalModel
	}
	if r.OpenAIAPIKey != "" {
		return modelDefaultOpenAI
	}
	if r.OpenAICodexAuthToken != "" {
		return normalizeCodexModel(modelDefaultCodex)
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
	// NB: must precede the prefix-based matchers — a local model may be named to
	// look like any provider's (e.g. "gpt-oss"), so an exact configured-model
	// match wins.
	case r.isLocalModel(model):
		endpoint, err = r.routeLocalModel()
		if err != nil {
			return nil, err
		}
	case r.isAnthropicModel(model):
		endpoint = r.routeAnthropicModel()
	// NB: must precede isOpenAIModel — a "codex"-named model (e.g. gpt-5.3-codex)
	// also matches the gpt- prefix; the codexModelPrefix form does not, but is
	// caught here too.
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
	// Strip the Codex routing prefix (if any) so the model displays and is sent
	// to the provider under its bare name; non-Codex models are unaffected.
	endpoint.Model = strings.TrimPrefix(model, codexModelPrefix)
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
		// OAuth (Claude Code subscription) bearer token, exported client-side
		// from the persisted llmconfig by `dagger llm`.
		return save("ANTHROPIC_AUTH_TOKEN", &r.AnthropicAuthToken)
	})
	eg.Go(func() error {
		return save("ANTHROPIC_THINKING_MODE", &r.AnthropicThinkingMode)
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
		// OAuth (ChatGPT subscription) bearer token for the Codex Responses API,
		// exported client-side from the persisted llmconfig by `dagger llm`.
		return save("OPENAI_CODEX_AUTH_TOKEN", &r.OpenAICodexAuthToken)
	})
	eg.Go(func() error {
		return save("OPENAI_CODEX_MODEL", &r.OpenAICodexModel)
	})
	eg.Go(func() error {
		return save("OPENAI_CODEX_THINKING_MODE", &r.OpenAICodexThinkingMode)
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
	eg.Go(func() error {
		return save("GEMINI_THINKING_MODE", &r.GeminiThinkingMode)
	})

	eg.Go(func() error {
		return save("LOCAL_BASE_URL", &r.LocalBaseURL)
	})
	eg.Go(func() error {
		return save("LOCAL_MODEL", &r.LocalModel)
	})
	eg.Go(func() error {
		return save("LOCAL_API_COMPAT", &r.LocalAPICompat)
	})
	eg.Go(func() error {
		return save("LOCAL_API_KEY", &r.LocalAPIKey)
	})

	var (
		openAIDisableStreaming  string
		anthropicThinkingBudget string
		geminiThinkingBudget    string
	)
	eg.Go(func() error {
		var err error
		openAIDisableStreaming, err = getenv(ctx, "OPENAI_DISABLE_STREAMING")
		return err
	})
	eg.Go(func() error {
		var err error
		anthropicThinkingBudget, err = getenv(ctx, "ANTHROPIC_THINKING_BUDGET")
		return err
	})
	eg.Go(func() error {
		var err error
		geminiThinkingBudget, err = getenv(ctx, "GEMINI_THINKING_BUDGET")
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

	if anthropicThinkingBudget != "" {
		v, err := strconv.ParseInt(anthropicThinkingBudget, 10, 64)
		if err != nil {
			return fmt.Errorf("parse ANTHROPIC_THINKING_BUDGET: %w", err)
		}
		r.AnthropicThinkingBudget = v
	}
	if geminiThinkingBudget != "" {
		v, err := strconv.ParseInt(geminiThinkingBudget, 10, 64)
		if err != nil {
			return fmt.Errorf("parse GEMINI_THINKING_BUDGET: %w", err)
		}
		r.GeminiThinkingBudget = v
	}

	// A bearer token implies OAuth (Claude Code) auth for Anthropic.
	r.AnthropicIsOAuth = r.AnthropicAuthToken != ""

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
	defer telemetry.EndWithCause(span, &rerr)
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

func (q *Query) NewLLM(ctx context.Context, model string) (*LLM, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	mcp := newMCP()
	// Bind the current workspace by default so the LLM's schema derives from its
	// own workspace (see MCP.Server), matching the CLI's view. Best-effort: a
	// context with no loaded workspace (ErrNoCurrentWorkspace) leaves the LLM
	// unbound and MCP.Server falls back to the current client's served deps. The
	// direct pre-check keeps the "no workspace" case from failing LLM creation
	// while still surfacing a genuine Select error. This is imperative (not
	// recorded as a .withWorkspace selector on the LLM ID), so it re-resolves to
	// the current workspace on history replay; an explicit LLM.withWorkspace still
	// pins a specific workspace via the ID.
	if _, err := q.CurrentWorkspace(ctx); err == nil {
		var ws dagql.ObjectResult[*Workspace]
		if err := srv.Select(ctx, srv.Root(), &ws, dagql.Selector{
			Field: "currentWorkspace",
		}); err != nil {
			return nil, err
		}
		mcp.workspace = ws
	} else if !errors.Is(err, ErrNoCurrentWorkspace) {
		return nil, err
	}
	return &LLM{
		model:       model,
		mcp:         mcp,
		endpointMtx: &sync.Mutex{},
	}, nil
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

var _ dagql.HasDependencyResults = (*LLM)(nil)

// AttachDependencyResults declares the results the LLM value embeds outside
// its call structure: the workspace it is bound to (captured imperatively by
// NewLLM and rebound by workspace-mutating tool results) and the objects bound
// as tools when a tool result rebound them mid-step (a withTools arg is
// already a structural dependency, but a rebind happens inside step execution).
// Declaring these edges lets the cache retain the embedded results and
// propagate their session-resource requirements — in particular, a
// client-owned workspace gates results embedding it to the session that
// created them (see WorkspaceClientHandle), so a new session re-resolves its
// own workspace instead of inheriting a dead client binding from cache.
func (llm *LLM) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	if llm == nil || llm.mcp == nil {
		return nil, nil
	}
	var deps []dagql.AnyResult
	if llm.mcp.workspace.Self() != nil {
		attached, err := attach(llm.mcp.workspace)
		if err != nil {
			return nil, fmt.Errorf("attach llm workspace: %w", err)
		}
		ws, ok := attached.(dagql.ObjectResult[*Workspace])
		if !ok {
			return nil, fmt.Errorf("attach llm workspace: unexpected result %T", attached)
		}
		llm.mcp.workspace = ws
		deps = append(deps, attached)
	}
	for i, bound := range llm.mcp.boundTools {
		if bound.Object == nil {
			continue
		}
		attached, err := attach(bound.Object)
		if err != nil {
			return nil, fmt.Errorf("attach llm bound tool object: %w", err)
		}
		obj, ok := attached.(dagql.AnyObjectResult)
		if !ok {
			return nil, fmt.Errorf("attach llm bound tool object: unexpected result %T", attached)
		}
		llm.mcp.boundTools[i].Object = obj
		deps = append(deps, attached)
	}
	return deps, nil
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

	// A local endpoint is reachable from the client's host, not necessarily the
	// engine (which may run in a container or on another host). Tunnel its
	// traffic through the client's session, then rebuild the client against the
	// rewritten (loopback) base URL.
	if endpoint.Provider == Local {
		parentClient, err := query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return nil, fmt.Errorf("local LLM: parent client metadata: %w", err)
		}
		tunnelCtx := engine.ContextWithClientMetadata(ctx, parentClient)
		tunnel, err := setupLocalTunnel(tunnelCtx, endpoint)
		if err != nil {
			return nil, fmt.Errorf("setup local LLM tunnel: %w", err)
		}
		endpoint.tunnel = tunnel
		switch router.LocalAPICompat {
		case "openai":
			endpoint.Client = newOpenAIClient(endpoint, "", false)
		case "anthropic":
			endpoint.Client = newAnthropicClient(endpoint)
		}
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
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	fileRes, err := dagql.NewObjectResultForCurrentCall(ctx, srv, file)
	if err != nil {
		return nil, err
	}
	contents, err := file.Contents(ctx, fileRes, nil, nil)
	if err != nil {
		return nil, err
	}
	return llm.WithPrompt(string(contents)), nil
}

// WithoutMessageHistory removes all messages, leaving only the system prompts
func (llm *LLM) WithoutMessageHistory() *LLM {
	llm = llm.Clone()
	llm.Messages = slices.DeleteFunc(llm.Messages, func(msg *LLMMessage) bool {
		return msg.Role != LLMMessageRoleSystem
	})
	return llm
}

// WithoutSystemPrompts removes all system prompts from the history, leaving
// only the default system prompt
func (llm *LLM) WithoutSystemPrompts() *LLM {
	llm = llm.Clone()
	llm.Messages = slices.DeleteFunc(llm.Messages, func(msg *LLMMessage) bool {
		return msg.Role == LLMMessageRoleSystem
	})
	return llm
}

// Append a system prompt message to the history
func (llm *LLM) WithSystemPrompt(prompt string) *LLM {
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

// WithToolCall appends a tool call to the last assistant message in the history.
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

// WithToolResponse appends a tool response (user) message to the history.
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

// WithTools binds an object so every eligible method becomes a tool
// (hack/designs/workspace-agents.md). A tool that returns the bound object's own type rebinds
// it as the new agent state; except lists method names to exclude (e.g. the
// module's own entrypoint).
func (llm *LLM) WithTools(obj dagql.AnyObjectResult, except []string) *LLM {
	llm = llm.Clone()
	llm.mcp = llm.mcp.WithTools(obj, except)
	return llm
}

// WithMaxTokens returns a new LLM with the maximum output tokens set.
// Zero means use provider defaults.
func (llm *LLM) WithMaxTokens(tokens int) *LLM {
	llm = llm.Clone()
	llm.maxTokens = tokens
	return llm
}

// Disable the default system prompt
func (llm *LLM) WithoutDefaultSystemPrompt() *LLM {
	llm = llm.Clone()
	llm.disableDefaultSystemPrompt = true
	return llm
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
	reply := "(no reply)"
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

func (llm *LLM) messagesWithSystemPrompt(ctx context.Context) ([]*LLMMessage, error) {
	var systemPrompt string
	if !llm.disableDefaultSystemPrompt {
		var err error
		systemPrompt, err = llm.mcp.DefaultSystemPrompt(ctx)
		if err != nil {
			return nil, err
		}
	}
	if systemPrompt != "" {
		return append([]*LLMMessage{{
			Role: LLMMessageRoleSystem,
			Content: []*LLMContentBlock{{
				Kind: LLMContentText,
				Text: systemPrompt,
			}},
		}}, llm.Messages...), nil
	}
	return llm.Messages, nil
}

type ModelFinishedError struct {
	Reason string
}

func (err *ModelFinishedError) Error() string {
	return fmt.Sprintf("model finished: %s", err.Reason)
}

// Step submits the queued prompt or tool-call results, evaluates any tool
// calls, and materializes the resulting message history through the API as a
// new LLM DAG node (via withResponse/withToolResponse/withWorkspace/withTools
// selectors).
func (llm *LLM) Step(ctx context.Context, inst dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	if err := llm.allowed(ctx); err != nil {
		return inst, err
	}
	return llm.step(ctx, inst)
}

//nolint:gocyclo // the step retry/error/streaming loop reads clearer as one function
func (llm *LLM) step(ctx context.Context, inst dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
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

	messagesToSend, err := llm.messagesWithSystemPrompt(ctx)
	if err != nil {
		return inst, err
	}

	var newMessages []*LLMMessage
	for _, msg := range slices.Backward(messagesToSend) {
		if msg.Role == LLMMessageRoleAssistant || msg.IsToolResult() {
			// only display messages appended since the last response
			break
		}
		newMessages = append(newMessages, msg)
	}
	slices.Reverse(newMessages)

	// Compute the LLM call digest for prompt/response span metadata. inst.ID()
	// is the LLM state entering step() (typically ends in withPrompt). Its
	// digest lets the TUI identify and branch from this point. Handle-form IDs
	// (post-evaluation cache handles) have no recipe digest, so skip the branch
	// attribute for them rather than panicking in Digest().
	var llmCallDigest string
	if id, idErr := inst.ID(); idErr == nil && !id.IsHandle() {
		llmCallDigest = id.Digest().String()
	}

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
		// The provider streams this turn's content into its own per-block display
		// spans (thinking, response, tool calls); it sets the call digest on them
		// so the TUI can branch from a span, and ends them (or the loop does for
		// text/thinking spans, once tool results are applied).
		res, sendErr = client.SendQuery(ctx, messagesToSend, tools, &LLMCallOpts{
			MaxTokens:  llm.maxTokens,
			CallDigest: llmCallDigest,
		})
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
		// An InputObject's fields are only populated by decoding a map through
		// its Decoder; a bare struct literal leaves them nil and panics when the
		// selector is serialized to a call literal. Decode from a map, mirroring
		// the pattern in core/schema/address.go. Field keys are the GraphQL arg
		// names (lowerCamel), and values must be types each field's decoder
		// accepts (enum → name string, JSON → string). "arguments" is always
		// present (empty decodes to nil and is skipped) since JSON is non-null.
		contentInputs := make(dagql.ArrayInput[dagql.InputObject[LLMContentBlockInput]], len(res.Content))
		for i, block := range res.Content {
			decoded, err := (dagql.InputObject[LLMContentBlockInput]{}).Decoder().DecodeInput(map[string]any{
				"kind":      string(block.Kind),
				"text":      block.Text,
				"callId":    block.CallID,
				"toolName":  block.ToolName,
				"arguments": string(block.Arguments),
				"errored":   block.Errored,
				"signature": block.Signature,
			})
			if err != nil {
				return inst, fmt.Errorf("decode content block %d input: %w", i, err)
			}
			input, ok := decoded.(dagql.InputObject[LLMContentBlockInput])
			if !ok {
				return inst, fmt.Errorf("decode content block %d input: unexpected type %T", i, decoded)
			}
			contentInputs[i] = input
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
	wsBefore, _ := llm.mcp.WorkspaceID()
	toolsBefore, _ := llm.mcp.BoundToolBindings()
	for _, msg := range llm.mcp.CallBatch(ctx, tools, toolCalls, res.ToolCallDisplays) {
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

	// Persist an in-step workspace change (e.g. a tool returned a Changeset that
	// was overlaid onto the bound workspace) so the edit survives the LLM history
	// rebuild — a rebuild otherwise re-binds the original workspace (via NewLLM or
	// the last recorded withWorkspace) and loses the overlay. Handle-safe compare
	// (post-eval IDs are handle-form).
	if wsAfter, err := llm.mcp.WorkspaceID(); err == nil && wsAfter != nil &&
		stableIDDigest(wsAfter) != stableIDDigest(wsBefore) {
		sels = append(sels, dagql.Selector{
			Field: "withWorkspace",
			Args: []dagql.NamedInput{
				{
					Name:  "workspace",
					Value: dagql.NewID[*Workspace](wsAfter),
				},
			},
		})
	}

	// Persist an in-step state transition: a tool that returned its bound object's
	// own type rebinds it (hack/designs/workspace-agents.md). Re-emit a withTools selector for
	// each binding whose object changed, so the new state survives the history
	// rebuild — the same shape as the withWorkspace persist above.
	if toolsAfter, err := llm.mcp.BoundToolBindings(); err == nil {
		for i, after := range toolsAfter {
			if i < len(toolsBefore) &&
				stableIDDigest(after.ID) == stableIDDigest(toolsBefore[i].ID) {
				continue
			}
			sels = append(sels, dagql.Selector{
				Field: "withTools",
				Args: []dagql.NamedInput{
					{
						Name:  "object",
						Value: dagql.NewAnyID(after.ID),
					},
					{
						Name:  "except",
						Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(after.Except...)),
					},
				},
			})
		}
	}

	// Tool-call display spans were already ended by CallBatch as each tool
	// returned. End the remaining (text/thinking) spans now that the turn's
	// results have been applied, so they close in the order they streamed.
	endedByCallBatch := make(map[trace.Span]bool, len(res.ToolCallDisplays))
	for _, tc := range res.ToolCallDisplays {
		endedByCallBatch[tc.Span] = true
	}
	endRemainingDisplaySpans := func() {
		for _, s := range res.DisplaySpans {
			if !endedByCallBatch[s] {
				s.End()
			}
		}
	}

	var stepped dagql.ObjectResult[*LLM]
	if err := srv.Select(ctx, inst, &stepped, sels...); err != nil {
		endRemainingDisplaySpans()
		return inst, err
	}
	endRemainingDisplaySpans()

	return stepped, nil
}

// Loop sends the context to the LLM endpoint, processes replies and tool calls,
// and continues in a loop until the model ends its turn (no more prompts) or
// the API call cap is reached.
func (llm *LLM) Loop(ctx context.Context, inst dagql.ObjectResult[*LLM], maxAPICalls int) (dagql.ObjectResult[*LLM], error) {
	if err := llm.allowed(ctx); err != nil {
		return inst, err
	}

	var apiCalls int
	for {
		llm := inst.Self()

		if !llm.HasPrompt() {
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
				// Context was cancelled (user interrupt). Return the last
				// successfully recorded state so that the prompt and any prior
				// progress are preserved in history.
				return inst, nil //nolint:nilerr // interrupt: preserve last state, no error
			}
			// Handle persistent error after all retries failed.
			return inst, err
		}
	}
}

func (llm *LLM) Interject(ctx context.Context, self dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], bool, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return self, false, err
	}
	bk, err := query.Engine(ctx)
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
	var selfDigest string
	if id, idErr := self.ID(); idErr == nil && !id.IsHandle() {
		selfDigest = id.Digest().String()
	}
	ctx, span := Tracer(ctx).Start(ctx, "LLM prompt", telemetry.Reveal(), trace.WithAttributes(
		attribute.String(telemetry.UIActorEmojiAttr, "🧑"),
		attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageSent),
		attribute.String(telemetry.LLMRoleAttr, telemetry.LLMRoleUser),
		attribute.String(telemetryattrs.LLMCallDigestAttr, selfDigest),
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

	src := module.Self().ContextSource.Value.Self()
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

	bk, err := query.Engine(ctx)
	if err != nil {
		return fmt.Errorf("llm sync failed fetching bk client for llm allow prompting: %w", err)
	}

	return bk.PromptAllowLLM(ctx, moduleURL)
}

// emitMessageSpan creates a telemetry span for a single LLM message. This is
// used both during live step() execution and during replay. callDigest is the
// DAG digest enabling TUI branching from that point.
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
		attrs = append(attrs, attribute.String(telemetryattrs.LLMCallDigestAttr, callDigest))
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
		kind   LLMContentBlockKind
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
				attrs = append(attrs, attribute.String(telemetryattrs.LLMCallDigestAttr, callDigest))
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

// SerializeHistory returns the message history as plain text suitable for LLM
// consumption (e.g. for summarization). Role-tagged lines with no emojis, tool
// calls shown as function signatures, and tool results included inline.
func (llm *LLM) SerializeHistory() string {
	var parts []string
	for _, msg := range llm.Messages {
		switch msg.Role {
		case LLMMessageRoleUser:
			for _, block := range msg.Content {
				switch block.Kind {
				case LLMContentToolResult:
					prefix := "[Tool result]"
					if block.Errored {
						prefix = "[Tool result ERROR]"
					}
					if block.Text != "" {
						parts = append(parts, prefix+": "+block.Text)
					}
				case LLMContentText:
					if block.Text != "" {
						parts = append(parts, "[User]: "+block.Text)
					}
				}
			}
		case LLMMessageRoleAssistant:
			var thinkingParts, textParts []string
			var toolCalls []string
			for _, block := range msg.Content {
				switch block.Kind {
				case LLMContentThinking:
					if block.Text != "" {
						thinkingParts = append(thinkingParts, block.Text)
					}
				case LLMContentText:
					if block.Text != "" {
						textParts = append(textParts, block.Text)
					}
				case LLMContentToolCall:
					toolCalls = append(toolCalls,
						fmt.Sprintf("%s(%s)", block.ToolName, string(block.Arguments)))
				}
			}
			if len(thinkingParts) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinkingParts, "\n"))
			}
			if len(textParts) > 0 {
				parts = append(parts, "[Assistant]: "+strings.Join(textParts, "\n"))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case LLMMessageRoleSystem:
			// System prompts are omitted from serialization
		}
	}
	return strings.Join(parts, "\n\n")
}

func (llm *LLM) WithWorkspace(ws dagql.ObjectResult[*Workspace]) *LLM {
	llm = llm.Clone()
	llm.mcp.workspace = ws
	return llm
}

func (llm *LLM) Workspace() dagql.ObjectResult[*Workspace] {
	return llm.mcp.workspace
}

// A variable in the LLM environment
type LLMVariable struct {
	// The name of the variable
	Name string `field:"true"`
	// The type name of the variable's value
	TypeName string `field:"true"`
	// A hash of the variable's value, used to detect changes
	Hash string `field:"true"`
}

var _ dagql.Typed = (*LLMVariable)(nil)
var _ dagql.PersistedObject = (*LLMVariable)(nil)
var _ dagql.PersistedObjectDecoder = (*LLMVariable)(nil)

func (v *LLMVariable) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMVariable",
		NonNull:   true,
	}
}

func (v *LLMVariable) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if v == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted LLM variable: nil LLM variable")
	}
	return encodePersistedObjectPayload(v)
}

func (*LLMVariable) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var v LLMVariable
	if err := json.Unmarshal(payload, &v); err != nil {
		return nil, fmt.Errorf("decode persisted LLM variable payload: %w", err)
	}
	return &v, nil
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

// ContextTokens returns the estimated number of tokens currently occupying the
// context window. Unlike TokenUsage, this is not cumulative over the whole
// session: it uses the last provider-reported assistant usage as a baseline and
// estimates any trailing messages (for example tool results) that have been
// appended since that response and will be sent with the next request.
func (llm *LLM) ContextTokens(ctx context.Context, dag *dagql.Server) (int, error) {
	_ = dag
	messages, err := llm.messagesWithSystemPrompt(ctx)
	if err != nil {
		return 0, err
	}
	return int(estimateContextTokens(messages)), nil
}

func estimateContextTokens(messages []*LLMMessage) int64 {
	lastUsageIndex := -1
	var tokens int64
	for i := len(messages) - 1; i >= 0; i-- {
		usage := messages[i].TokenUsage
		if usage.hasTokens() {
			lastUsageIndex = i
			tokens = usage.contextTokens()
			break
		}
	}

	for i := lastUsageIndex + 1; i < len(messages); i++ {
		tokens += messages[i].estimateTokens()
	}
	return tokens
}
