package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"dagger.io/dagger/telemetry"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/iancoleman/strcase"
	"github.com/joho/godotenv"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
)

func init() {
	strcase.ConfigureAcronym("LLM", "LLM")
}

const (
	modelDefaultAnthropic = anthropic.ModelClaude3_5SonnetLatest
	modelDefaultGoogle    = "gemini-2.0-flash"
	modelDefaultOpenAI    = "gpt-4o"
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
	Query *Query

	maxAPICalls int
	apiCalls    int
	Endpoint    *LLMEndpoint

	once *sync.Once

	// History of messages
	messages []ModelMessage

	// The environment accessible to the LLM, exposed over MCP
	mcp *MCP
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
	SendQuery(ctx context.Context, history []ModelMessage, tools []LLMTool) (*LLMResponse, error)
}

type LLMResponse struct {
	Content    string
	ToolCalls  []ToolCall
	TokenUsage LLMTokenUsage
}

type LLMTokenUsage struct {
	InputTokens  int64 `field:"true"`
	OutputTokens int64 `field:"true"`
	TotalTokens  int64 `field:"true"`
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
	ToolCalls   []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID  string        `json:"tool_call_id,omitempty"`
	ToolErrored bool          `json:"tool_errored,omitempty"`
	TokenUsage  LLMTokenUsage `json:"token_usage,omitempty"`
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

func (r *LLMRouter) getReplay(model string) (messages []ModelMessage, _ error) {
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
	var openAIDisableStreaming string
	openAIDisableStreaming, err = getenv(ctx, "OPENAI_DISABLE_STREAMING")
	if err != nil {
		return err
	}
	if openAIDisableStreaming != "" {
		r.OpenAIDisableStreaming, err = strconv.ParseBool(openAIDisableStreaming)
		if err != nil {
			return err
		}
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

func NewLLM(ctx context.Context, query *Query, model string, maxAPICalls int) (*LLM, error) {
	router, err := loadLLMRouter(ctx, query)
	if err != nil {
		return nil, err
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
	return &LLM{
		Query:       query,
		Endpoint:    endpoint,
		maxAPICalls: maxAPICalls,
		mcp:         NewEnv().MCP(endpoint),
		once:        &sync.Once{},
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
	cp.messages = cloneSlice(cp.messages)
	cp.mcp = cp.mcp.Clone()
	cp.once = &sync.Once{}
	return &cp
}

// Generate a human-readable documentation of tools available to the model
func (llm *LLM) ToolsDoc(ctx context.Context, srv *dagql.Server) (string, error) {
	tools, err := llm.mcp.Tools(srv)
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

func (llm *LLM) WithModel(ctx context.Context, model string, srv *dagql.Server) (*LLM, error) {
	// FIXME: mcp implementation takes hints from endpoint: reconfigure it
	llm = llm.Clone()
	router, err := NewLLMRouter(ctx, srv)
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
func (llm *LLM) WithPrompt(
	ctx context.Context,
	// The prompt message.
	prompt string,
	srv *dagql.Server,
) (*LLM, error) {
	prompt = os.Expand(prompt, func(key string) string {
		if binding, found := llm.mcp.env.Input(key); found {
			return binding.String()
		}
		// leave unexpanded, perhaps it refers to an object var
		return fmt.Sprintf("$%s", key)
	})
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
	llm.messages = append(llm.messages, ModelMessage{
		Role:    "user",
		Content: prompt,
	})
	return llm, nil
}

// WithPromptFile is like WithPrompt but reads the prompt from a file
func (llm *LLM) WithPromptFile(ctx context.Context, file *File, srv *dagql.Server) (*LLM, error) {
	contents, err := file.Contents(ctx)
	if err != nil {
		return nil, err
	}
	return llm.WithPrompt(ctx, string(contents), srv)
}

// Append a system prompt message to the history
func (llm *LLM) WithSystemPrompt(prompt string) *LLM {
	llm = llm.Clone()
	llm.messages = append(llm.messages, ModelMessage{
		Role:    "system",
		Content: prompt,
	})
	return llm
}

// Return the last message sent by the agent
func (llm *LLM) LastReply(ctx context.Context, dag *dagql.Server) (string, error) {
	if err := llm.Sync(ctx, dag); err != nil {
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

func (llm *LLM) messagesWithSystemPrompt() []ModelMessage {
	var hasSystemPrompt bool
	for _, env := range llm.messages {
		if env.Role == "system" {
			return llm.messages
		}
	}

	messages := llm.messages

	// inject default system prompt if none are found
	if prompt := llm.mcp.DefaultSystemPrompt(); prompt != "" && !hasSystemPrompt {
		return append([]ModelMessage{
			{
				Role:    "system",
				Content: prompt,
			},
		}, messages...)
	}

	return messages
}

type ModelFinishedError struct {
	Reason string
}

func (err *ModelFinishedError) Error() string {
	return fmt.Sprintf("model finished: %s", err.Reason)
}

// send the context to the LLM endpoint, process replies and tool calls; continue in a loop
// Synchronize LLM state:
// 1. Send context to LLM endpoint
// 2. Process replies and tool calls
// 3. Continue in a loop until no tool calls, or caps are reached
func (llm *LLM) Sync(ctx context.Context, dag *dagql.Server) error {
	if err := llm.allowed(ctx); err != nil {
		return err
	}
	var err error
	llm.once.Do(func() {
		err = llm.loop(ctx, dag)
	})
	return err
}

func (llm *LLM) Interject(ctx context.Context) error {
	bk, err := llm.Query.Buildkit(ctx)
	if err != nil {
		return err
	}
	ctx, span := Tracer(ctx).Start(ctx, "LLM prompt", telemetry.Reveal(), trace.WithAttributes(
		attribute.String(telemetry.UIActorEmojiAttr, "🧑"),
		attribute.String(telemetry.UIMessageAttr, "sent"),
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
	msg, err := bk.PromptHumanHelp(ctx, fmt.Sprintf("The LLM was unable to complete its task and needs help. Here is its last message:\n%s", mdQuote(lastAssistantMessage)))
	if err != nil {
		return err
	}
	if msg == "" {
		return errors.New("no interjection provided; giving up")
	}
	fmt.Fprint(stdio.Stdout, msg)
	llm.messages = append(llm.messages, ModelMessage{
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
	bk, err := llm.Query.Buildkit(ctx)
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

func (llm *LLM) loop(ctx context.Context, dag *dagql.Server) error {
	if len(llm.messages) == 0 {
		// dirty but no messages, possibly just a state change, nothing to do
		// until a prompt is given
		return nil
	}
	for {
		if llm.maxAPICalls > 0 && llm.apiCalls >= llm.maxAPICalls {
			return fmt.Errorf("reached API call limit: %d", llm.apiCalls)
		}
		llm.apiCalls++

		tools, err := llm.mcp.Tools(dag)
		if err != nil {
			return err
		}

		res, err := llm.Endpoint.Client.SendQuery(ctx, llm.messagesWithSystemPrompt(), tools)
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
			return err
		}

		// Add the model reply to the history
		llm.messages = append(llm.messages, ModelMessage{
			Role:       "assistant",
			Content:    res.Content,
			ToolCalls:  res.ToolCalls,
			TokenUsage: res.TokenUsage,
		})
		// Handle tool calls
		// calls := res.Choices[0].Message.ToolCalls
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
		for _, toolCall := range res.ToolCalls {
			content, isError := llm.mcp.Call(ctx, tools, toolCall)
			llm.messages = append(llm.messages, ModelMessage{
				Role:        "user", // Anthropic only allows tool calls in user messages
				Content:     content,
				ToolCallID:  toolCall.ID,
				ToolErrored: isError,
			})
		}
	}
	return nil
}

func (llm *LLM) allowed(ctx context.Context) error {
	module, err := llm.Query.CurrentModule(ctx)
	if err != nil {
		// allow non-module calls
		if errors.Is(err, ErrNoCurrentModule) {
			return nil
		}
		return fmt.Errorf("failed to figure out module while deciding if llm is allowed: %w", err)
	}
	if module.Source.Self.Kind != ModuleSourceKindGit {
		return nil
	}

	md, err := engine.ClientMetadataFromContext(ctx) // not mainclient
	if err != nil {
		return fmt.Errorf("llm sync failed fetching client metadata from context: %w", err)
	}

	moduleURL := module.Source.Self.Git.Symbolic
	for _, allowedModule := range md.AllowedLLMModules {
		if allowedModule == "all" || moduleURL == allowedModule {
			return nil
		}
	}

	bk, err := llm.Query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("llm sync failed fetching bk client for llm allow prompting: %w", err)
	}

	return bk.PromptAllowLLM(ctx, moduleURL)
}

func (llm *LLM) History(ctx context.Context, dag *dagql.Server) ([]string, error) {
	if err := llm.Sync(ctx, dag); err != nil {
		return nil, err
	}
	var history []string
	for _, msg := range llm.messages {
		content := strings.TrimRight(msg.Content, "\n")
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

func (llm *LLM) HistoryJSON(ctx context.Context, dag *dagql.Server) (string, error) {
	if err := llm.Sync(ctx, dag); err != nil {
		return "", err
	}
	result, err := json.MarshalIndent(llm.messages, "", "  ")
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (llm *LLM) WithEnv(env *Env) *LLM {
	llm = llm.Clone()
	llm.mcp = env.MCP(llm.Endpoint)
	return llm
}

func (llm *LLM) Env() *Env {
	return llm.mcp.env
}

func (llm *LLM) With(value dagql.Object) *LLM {
	llm = llm.Clone()
	llm.mcp.Select(value)
	return llm
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
	if err := llm.Sync(ctx, dag); err != nil {
		return res, err
	}
	if llm.mcp.Current() == nil {
		return res, nil
	}
	res.Value = &Binding{
		Key:   name,
		Value: llm.mcp.Current(),
		env:   llm.mcp.env,
	}
	res.Valid = true
	return res, nil
}

func (llm *LLM) TokenUsage(ctx context.Context, dag *dagql.Server) (*LLMTokenUsage, error) {
	if err := llm.Sync(ctx, dag); err != nil {
		return nil, err
	}
	var res LLMTokenUsage
	for _, msg := range llm.messages {
		res.InputTokens += msg.TokenUsage.InputTokens
		res.OutputTokens += msg.TokenUsage.OutputTokens
		res.TotalTokens += msg.TokenUsage.TotalTokens
	}
	return &res, nil
}
