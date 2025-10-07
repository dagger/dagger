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
	// The environment accessible to the LLM, exposed over MCP
	mcp *MCP

	maxAPICalls int
	apiCalls    int

	model string

	endpoint    *LLMEndpoint
	endpointMtx *sync.Mutex

	syncOneStep bool
	once        *sync.Once
	err         error

	// History of messages
	messages []*ModelMessage

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
	SendQuery(ctx context.Context, history []*ModelMessage, tools []LLMTool) (*LLMResponse, error)
	IsRetryable(err error) bool
}

type LLMResponse struct {
	Content    string
	ToolCalls  []LLMToolCall
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

// ModelMessage represents a generic message in the LLM conversation
type ModelMessage struct {
	Role        string        `json:"role"`
	Content     string        `json:"content"`
	ToolCalls   []LLMToolCall `json:"tool_calls,omitempty"`
	ToolCallID  string        `json:"tool_call_id,omitempty"`
	ToolErrored bool          `json:"tool_errored,omitempty"`
	TokenUsage  LLMTokenUsage `json:"token_usage,omitzero"`
}

type LLMToolCall struct {
	ID       string   `json:"id"`
	Function FuncCall `json:"function"`
	Type     string   `json:"type"`
}

type FuncCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
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

func (r *LLMRouter) getReplay(model string) (messages []*ModelMessage, _ error) {
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
		once:        &sync.Once{},
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
	cp.messages = slices.Clone(cp.messages)
	cp.mcp = cp.mcp.Clone()
	cp.endpoint = llm.endpoint
	cp.endpointMtx = &sync.Mutex{}
	cp.once = &sync.Once{}
	cp.err = nil
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
	llm.messages = append(llm.messages, &ModelMessage{
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
	llm.messages = append(llm.messages, &ModelMessage{
		Role:    "system",
		Content: prompt,
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
	if err := llm.Sync(ctx); err != nil {
		return "", err
	}
	var reply string = "(no reply)"
	for _, msg := range llm.messages {
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

func (llm *LLM) messagesWithSystemPrompt() []*ModelMessage {
	var systemPrompt string
	if !llm.disableDefaultSystemPrompt {
		systemPrompt = llm.mcp.DefaultSystemPrompt()
	}
	if systemPrompt != "" {
		return append([]*ModelMessage{{
			Role:    "system",
			Content: systemPrompt,
		}}, llm.messages...)
	}
	return llm.messages
}

type ModelFinishedError struct {
	Reason string
}

func (err *ModelFinishedError) Error() string {
	return fmt.Sprintf("model finished: %s", err.Reason)
}

// Send configures the LLM to only evaluate one step when syncing.
func (llm *LLM) Step() *LLM {
	llm = llm.Clone()
	llm.syncOneStep = true
	return llm
}

// send the context to the LLM endpoint, process replies and tool calls; continue in a loop
// Synchronize LLM state:
// 1. Send context to LLM endpoint
// 2. Process replies and tool calls
// 3. Continue in a loop until no tool calls, or caps are reached
func (llm *LLM) Sync(ctx context.Context) error {
	if err := llm.allowed(ctx); err != nil {
		return err
	}
	llm.once.Do(func() {
		err := llm.loop(ctx)
		if err != nil && ctx.Err() == nil {
			// Consider an interrupt to be successful, so we can still use the result
			// of a partially completed sequence (e.g. accessing its Env). The user
			// must append another prompt to interject and continue. (This matches the
			// behavior of Claude Code and presumably other chat agents.)
			llm.err = err
		}
	})
	return llm.err
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
	for i := len(llm.messages) - 1; i >= 0; i-- {
		if llm.messages[i].Role == "assistant" {
			lastAssistantMessage = llm.messages[i].Content
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
	llm.messages = append(llm.messages, &ModelMessage{
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

func (llm *LLM) loop(ctx context.Context) error {
	var hasUserMessage bool
	for _, message := range llm.messages {
		if message.Role == "user" {
			hasUserMessage = true
			break
		}
	}
	if !hasUserMessage {
		// dirty but no messages, possibly just a state change, nothing to do
		// until a prompt is given
		return nil
	}

	b := backoff.NewExponentialBackOff()
	// Sane defaults (ideally not worth extra knobs)
	b.InitialInterval = 1 * time.Second
	b.MaxInterval = 30 * time.Second
	b.MaxElapsedTime = 2 * time.Minute

	for {
		if llm.maxAPICalls > 0 && llm.apiCalls >= llm.maxAPICalls {
			return fmt.Errorf("reached API call limit: %d", llm.apiCalls)
		}
		llm.apiCalls++

		tools, err := llm.mcp.Tools(ctx)
		if err != nil {
			return err
		}

		messagesToSend := llm.messagesWithSystemPrompt()

		var newMessages []*ModelMessage
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
				case "user":
					emoji = "🧑"
				case "system":
					emoji = "⚙️"
				}
				ctx, span := Tracer(ctx).Start(ctx, "LLM prompt",
					telemetry.Reveal(),
					trace.WithAttributes(
						attribute.String(telemetry.UIActorEmojiAttr, emoji),
						attribute.String(telemetry.UIMessageAttr, telemetry.UIMessageSent),
						attribute.String(telemetry.LLMRoleAttr, msg.Role),
						attribute.Bool(telemetry.UIInternalAttr, msg.Role == "system"),
					))
				defer span.End()
				stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
					log.String(telemetry.ContentTypeAttr, "text/markdown"))
				defer stdio.Close()
				fmt.Fprint(stdio.Stdout, msg.Content)
			}()
		}

		var res *LLMResponse

		// Retry operation
		ep, err := llm.Endpoint(ctx)
		if err != nil {
			return err
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

		// Check the final error after retries (if any)
		if err != nil {
			var finished *ModelFinishedError
			if errors.As(err, &finished) {
				if interjected, interjectErr := llm.autoInterject(ctx); interjectErr != nil {
					// interjecting failed or was interrupted
					return errors.Join(err, interjectErr)
				} else if interjected {
					// interjected - continue
					continue
				} else {
					// no interjection and none needed - we're just done
					break
				}
			}
			// Handle persistent error after all retries failed.
			return fmt.Errorf("not retrying: %w", err)
		}

		// Add the model reply to the history
		llm.messages = append(llm.messages, &ModelMessage{
			Role:       "assistant",
			Content:    res.Content,
			ToolCalls:  res.ToolCalls,
			TokenUsage: res.TokenUsage,
		})

		// Handle tool calls
		if len(res.ToolCalls) == 0 {
			if interjected, interjectErr := llm.autoInterject(ctx); interjectErr != nil {
				// interjecting failed or was interrupted
				return interjectErr
			} else if interjected {
				// interjected - continue
				continue
			}
			// no interjection and none needed - we're just done
			break
		}

		// Run tool calls in batch with efficient MCP syncing
		llm.messages = append(llm.messages, llm.mcp.CallBatch(ctx, tools, res.ToolCalls)...)

		if llm.mcp.Returned() {
			// we returned; exit the loop, since some models just keep going
			break
		}
		if llm.syncOneStep {
			// we're configured to only do one step; return early
			return nil
		}
	}
	return nil
}

func (llm *LLM) HasPrompt() bool {
	return len(llm.messages) > 0 && llm.messages[len(llm.messages)-1].Role == "user"
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
	if err := llm.Sync(ctx); err != nil {
		return nil, err
	}
	var history []string
	var lastRole string
	for _, msg := range llm.messages {
		if len(history) > 0 && lastRole != msg.Role {
			// add a blank line when roles change
			history = append(history, "")
			lastRole = msg.Role
		}
		content := squash(msg.Content)
		switch msg.Role {
		case "user":
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
		case "assistant":
			if len(content) > 0 {
				history = append(history, "🤖 💬 "+content)
			}
			for _, call := range msg.ToolCalls {
				args, err := json.Marshal(call.Function.Arguments)
				if err != nil {
					return nil, err
				}
				item := fmt.Sprintf("🤖 🛠️ %s %s", call.Function.Name, args)
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
	if err := llm.Sync(ctx); err != nil {
		return nil, err
	}
	result, err := json.MarshalIndent(llm.messages, "", "  ")
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

func (v *LLMVariable) Type() *ast.Type {
	return &ast.Type{
		NamedType: "LLMVariable",
		NonNull:   true,
	}
}

func (llm *LLM) BindResult(ctx context.Context, dag *dagql.Server, name string) (dagql.Nullable[*Binding], error) {
	var res dagql.Nullable[*Binding]
	if err := llm.Sync(ctx); err != nil {
		return res, err
	}
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
	if err := llm.Sync(ctx); err != nil {
		return nil, err
	}
	var res LLMTokenUsage
	for _, msg := range llm.messages {
		res.InputTokens += msg.TokenUsage.InputTokens
		res.OutputTokens += msg.TokenUsage.OutputTokens
		res.CachedTokenReads += msg.TokenUsage.CachedTokenReads
		res.CachedTokenWrites += msg.TokenUsage.CachedTokenWrites
		res.TotalTokens += msg.TokenUsage.TotalTokens
	}
	return &res, nil
}
