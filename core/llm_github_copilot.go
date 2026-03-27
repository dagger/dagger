package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"

	copilot "github.com/github/copilot-sdk/go"
	telemetry "github.com/dagger/otel-go"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	// ghcpDefaultCLIVersion is the Copilot CLI version bundled with SDK v0.2.0.
	// Bump this alongside go.mod when upgrading the SDK.
	ghcpDefaultCLIVersion = "1.0.10"
	ghcpDefaultCLIPort    = 3000
)

type GhcpClient struct {
	endpoint     *LLMEndpoint
	cliVersion   string
	svc          *dagger.Service  // the copilot CLI sidecar
	client       *copilot.Client  // SDK client connected via CLIUrl
	session      *copilot.Session // persistent session (multi-turn)
	mu           sync.Mutex       // protects svc, client, session, sentMsgCount, toolsHash
	sentMsgCount int              // number of history messages already sent to this session
	toolsHash    string           // fingerprint of registered tools; triggers session recreation on change
}

var _ LLMClient = (*GhcpClient)(nil)

var gitHubModelPrefixes = []string{
	"github-",
	"github/",
	"gh-",
	"gh/",
	"ghcp-",
	"ghcp/",
}

// StripGitHubModelPrefix strips provider-specific prefixes from GitHub model names.
// E.g. "github-gpt-5" -> "gpt-5". Called from core/llm.go.
func StripGitHubModelPrefix(model string) string {
	for _, prefix := range gitHubModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return strings.TrimPrefix(model, prefix)
		}
	}
	return model
}

// copilotSidecar builds the Copilot CLI as an on-demand Dagger service.
// The tarball is fetched via dag.HTTP so Dagger's content-addressable cache
// ensures it is downloaded only once per version.
func copilotSidecar(token, cliVersion string) *dagger.Service {
	version := cliVersion
	if v := os.Getenv("GITHUB_CLI_VERSION"); v != "" {
		version = v
	}
	if version == "" {
		version = ghcpDefaultCLIVersion
	}

	tarballURL := os.Getenv("GITHUB_COPILOT_CLI_URL")
	if tarballURL == "" {
		platform := "linux-x64"
		if runtime.GOARCH == "arm64" {
			platform = "linux-arm64"
		}
		tarballURL = fmt.Sprintf(
			"https://registry.npmjs.org/@github/copilot-%s/-/copilot-%s-%s.tgz",
			platform, platform, version,
		)
	}

	tarball := dag.HTTP(tarballURL)
	return dag.Container().
		From("debian:bookworm-slim").
		WithMountedFile("/tmp/copilot.tgz", tarball).
		WithExec([]string{"sh", "-c",
			"tar -xzf /tmp/copilot.tgz -C /tmp && " +
				"mv /tmp/package/copilot /usr/local/bin/copilot && " +
				"chmod +x /usr/local/bin/copilot && " +
				"rm -rf /tmp/copilot.tgz /tmp/package"}).
		WithEnvVariable("GITHUB_TOKEN", token).
		WithExposedPort(ghcpDefaultCLIPort).
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{
				"copilot", "--headless", "--no-auto-update",
				"--port", strconv.Itoa(ghcpDefaultCLIPort),
			},
		})
}

// newGhcpClient creates a GhcpClient. The sidecar and SDK connection are
// established lazily on the first SendQuery call.
func newGhcpClient(endpoint *LLMEndpoint, cliVersion string) *GhcpClient {
	if cliVersion == "" {
		cliVersion = ghcpDefaultCLIVersion
	}
	return &GhcpClient{
		endpoint:   endpoint,
		cliVersion: cliVersion,
	}
}

// ensureConnected starts the sidecar service and connects the SDK client.
// It is idempotent: subsequent calls return immediately once connected.
func (c *GhcpClient) ensureConnected(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return nil
	}

	svc := copilotSidecar(c.endpoint.Key, c.cliVersion)
	startedSvc, err := svc.Start(ctx)
	if err != nil {
		return fmt.Errorf("start copilot sidecar: %w", err)
	}
	c.svc = startedSvc

	// Endpoint returns host:port accessible from the engine process.
	addr, err := startedSvc.Endpoint(ctx, dagger.ServiceEndpointOpts{Port: ghcpDefaultCLIPort})
	if err != nil {
		return fmt.Errorf("get copilot sidecar endpoint: %w", err)
	}

	// NOTE: GitHubToken must NOT be set here with CLIUrl — the SDK panics.
	// Auth is handled by the sidecar via its GITHUB_TOKEN env var.
	sdkClient := copilot.NewClient(&copilot.ClientOptions{CLIUrl: addr})
	if err := sdkClient.Start(ctx); err != nil {
		return fmt.Errorf("start copilot SDK client: %w", err)
	}
	c.client = sdkClient
	return nil
}

// providerConfig returns a ProviderConfig for BYOK if endpoint.BaseURL is set,
// or if GITHUB_COPILOT_PROVIDER_URL env var is set.
// Returns nil if using the default GitHub Copilot backend.
func (c *GhcpClient) providerConfig() *copilot.ProviderConfig {
	baseURL := c.endpoint.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("GITHUB_COPILOT_PROVIDER_URL")
	}
	if baseURL == "" {
		return nil
	}

	cfg := &copilot.ProviderConfig{
		BaseURL: baseURL,
	}

	switch {
	case strings.Contains(baseURL, ".openai.azure.com"):
		cfg.Type = "azure"
	case strings.Contains(baseURL, "anthropic"):
		cfg.Type = "anthropic"
	default:
		cfg.Type = "openai" // covers OpenAI, Ollama, LM Studio, etc.
	}

	if c.endpoint.Key != "" {
		cfg.APIKey = c.endpoint.Key
	}

	return cfg
}

// getOrCreateSession returns the persistent session, creating it if needed.
// systemPrompt is passed to SessionConfig.SystemMessage on first creation only.
// If the registered tool set changes (by name fingerprint), the old session is
// closed and a new one is created so the new tools are available.
func (c *GhcpClient) getOrCreateSession(ctx context.Context, systemPrompt string, tools []copilot.Tool) (*copilot.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	newHash := hashTools(tools)
	if c.session != nil && c.toolsHash != newHash {
		c.session.Disconnect() //nolint:errcheck
		c.session = nil
		c.sentMsgCount = 0
	}

	if c.session != nil {
		return c.session, nil
	}

	var sysMsg *copilot.SystemMessageConfig
	if systemPrompt != "" {
		sysMsg = &copilot.SystemMessageConfig{Content: systemPrompt}
	}

	session, err := c.client.CreateSession(ctx, &copilot.SessionConfig{
		Streaming:           true, // required for message_delta events
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		Tools:               tools,
		SystemMessage:       sysMsg,
		Provider:            c.providerConfig(),
		Model:               StripGitHubModelPrefix(c.endpoint.Model),
	})
	if err != nil {
		return nil, fmt.Errorf("create copilot session: %w", err)
	}
	c.session = session
	c.toolsHash = newHash
	return session, nil
}

// SendQuery sends only the new messages (since the last call) to Copilot and
// returns the response. The Copilot CLI maintains full conversation history
// server-side within the session, so we track a cursor (sentMsgCount) and
// send only the delta each time.
func (c *GhcpClient) SendQuery(ctx context.Context, history []*ModelMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {
	copilotModel := StripGitHubModelPrefix(c.endpoint.Model)

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary,
		log.String(telemetry.ContentTypeAttr, "text/markdown"))
	defer stdio.Close()

	m := telemetry.Meter(ctx, InstrumentationLibrary)
	spanCtx := trace.SpanContextFromContext(ctx)

	attrs := []attribute.KeyValue{
		attribute.String(telemetry.MetricsTraceIDAttr, spanCtx.TraceID().String()),
		attribute.String(telemetry.MetricsSpanIDAttr, spanCtx.SpanID().String()),
		attribute.String("model", copilotModel),
		attribute.String("provider", string(c.endpoint.Provider)),
	}

	inputTokensGauge, err := m.Int64Gauge(telemetry.LLMInputTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to get inputTokens gauge: %w", err)
	}
	inputTokensCacheReadsGauge, err := m.Int64Gauge(telemetry.LLMInputTokensCacheReads)
	if err != nil {
		return nil, fmt.Errorf("failed to get inputTokensCacheReads gauge: %w", err)
	}
	outputTokensGauge, err := m.Int64Gauge(telemetry.LLMOutputTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to get outputTokens gauge: %w", err)
	}

	if len(history) == 0 {
		return nil, fmt.Errorf("prompt/chat history cannot be empty - run with-prompt to add a prompt/message")
	}

	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	sdkTools := buildSDKTools(tools)

	// Extract system prompt for new session creation (only used on first call).
	systemPrompt := ""
	for _, msg := range history {
		if msg.Role == "system" {
			systemPrompt = msg.Content
			break
		}
	}

	// Phase 2: send only new messages, retry once on session expiry.
	var (
		sendErr error
		usage   LLMTokenUsage
		usageMu sync.Mutex

		toolCallsMu   sync.Mutex
		capturedCalls []LLMToolCall

		contentMu   sync.Mutex
		fullContent strings.Builder
	)

	for attempt := 0; attempt < 2; attempt++ {
		session, err := c.getOrCreateSession(ctx, systemPrompt, sdkTools)
		if err != nil {
			return nil, err
		}

		// Read cursor under lock so it's consistent with the session.
		c.mu.Lock()
		startIdx := c.sentMsgCount
		c.mu.Unlock()

		// Identify messages not yet sent to this session.
		newMsgs := history[startIdx:]
		prompt := ""
		for _, msg := range newMsgs {
			if msg.Role == "user" {
				prompt = msg.Content
			}
		}
		if prompt == "" {
			return nil, fmt.Errorf("no user message found in new history")
		}

		// Reset per-attempt state.
		usage = LLMTokenUsage{}
		fullContent.Reset()
		toolCallsMu.Lock()
		capturedCalls = capturedCalls[:0]
		toolCallsMu.Unlock()

		// Capture token usage from assistant.usage events, which fire before session.idle.
		unsubUsage := session.On(func(event copilot.SessionEvent) {
			if event.Type != copilot.SessionEventTypeAssistantUsage {
				return
			}
			usageMu.Lock()
			defer usageMu.Unlock()
			if event.Data.InputTokens != nil {
				usage.InputTokens = int64(*event.Data.InputTokens)
			}
			if event.Data.OutputTokens != nil {
				usage.OutputTokens = int64(*event.Data.OutputTokens)
			}
			if event.Data.CacheReadTokens != nil {
				usage.CachedTokenReads = int64(*event.Data.CacheReadTokens)
			}
			if event.Data.CacheWriteTokens != nil {
				usage.CachedTokenWrites = int64(*event.Data.CacheWriteTokens)
			}
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		})

		// Capture tool call metadata so Dagger can display them and track history.
		// The SDK auto-executes tools via Tool.Handler during SendAndWait; we record
		// each invocation here for Dagger's TUI and cursor accounting.
		unsubTools := session.On(func(event copilot.SessionEvent) {
			if event.Type != copilot.SessionEventTypeExternalToolRequested {
				return
			}
			toolCallID := ""
			if event.Data.ToolCallID != nil {
				toolCallID = *event.Data.ToolCallID
			}
			toolName := ""
			if event.Data.ToolName != nil {
				toolName = *event.Data.ToolName
			}
			var args map[string]any
			if event.Data.Arguments != nil {
				if m, ok := event.Data.Arguments.(map[string]any); ok {
					args = m
				}
			}
			toolCallsMu.Lock()
			defer toolCallsMu.Unlock()
			capturedCalls = append(capturedCalls, LLMToolCall{
				ID:   toolCallID,
				Type: "function",
				Function: FuncCall{
					Name:      toolName,
					Arguments: args,
				},
			})
		})

		idleCh := make(chan struct{}, 1)
		streamErrCh := make(chan error, 1)

		unsubStream := session.On(func(event copilot.SessionEvent) {
			switch event.Type {
			case copilot.SessionEventTypeAssistantMessageDelta:
				if event.Data.DeltaContent != nil {
					delta := *event.Data.DeltaContent
					contentMu.Lock()
					fullContent.WriteString(delta)
					contentMu.Unlock()
					fmt.Fprint(stdio.Stdout, delta)
				}
			case copilot.SessionEventTypeSessionIdle:
				select {
				case idleCh <- struct{}{}:
				default:
				}
			case copilot.SessionEventTypeSessionError:
				errMsg := "session error"
				if event.Data.Message != nil {
					errMsg = *event.Data.Message
				}
				select {
				case streamErrCh <- fmt.Errorf("copilot session error: %s", errMsg):
				default:
				}
			}
		})

		_, sendErr = session.Send(ctx, copilot.MessageOptions{Prompt: prompt})
		if sendErr == nil {
			waitCtx := ctx
			if _, ok := ctx.Deadline(); !ok {
				var cancel context.CancelFunc
				waitCtx, cancel = context.WithTimeout(ctx, 120*time.Second)
				defer cancel()
			}
			select {
			case <-idleCh:
				// done
			case streamErr := <-streamErrCh:
				sendErr = streamErr
			case <-waitCtx.Done():
				sendErr = waitCtx.Err()
			}
		}

		unsubUsage()
		unsubTools()
		unsubStream()

		if sendErr != nil && isSessionExpired(sendErr) && attempt == 0 {
			// Session gone server-side — clear state and retry with a fresh session.
			c.mu.Lock()
			c.session = nil
			c.sentMsgCount = 0
			c.mu.Unlock()
			continue
		}
		break
	}

	if sendErr != nil {
		return nil, fmt.Errorf("copilot send: %w", sendErr)
	}

	// Advance cursor: all messages in history are now acknowledged by the server.
	// Dagger will append the assistant response after we return, so the next call
	// will see len(history)+1 messages and send only the new user turn.
	c.mu.Lock()
	c.sentMsgCount = len(history)
	c.mu.Unlock()

	contentMu.Lock()
	content := fullContent.String()
	contentMu.Unlock()

	toolCallsMu.Lock()
	toolCalls := make([]LLMToolCall, len(capturedCalls))
	copy(toolCalls, capturedCalls)
	toolCallsMu.Unlock()

	usageMu.Lock()
	finalUsage := usage
	usageMu.Unlock()

	inputTokensGauge.Record(ctx, finalUsage.InputTokens, metric.WithAttributes(attrs...))
	outputTokensGauge.Record(ctx, finalUsage.OutputTokens, metric.WithAttributes(attrs...))
	inputTokensCacheReadsGauge.Record(ctx, finalUsage.CachedTokenReads, metric.WithAttributes(attrs...))

	return &LLMResponse{
		Content:    content,
		ToolCalls:  toolCalls,
		TokenUsage: finalUsage,
	}, nil
}

var ghcpRetryable = []string{
	// HTTP status codes surfaced in error messages
	"429",
	"500",
	"503",
	"504",
	// Copilot-specific messages
	"rate limit",
	"rate_limit",
	"quota exceeded",
	"overloaded",
	"capacity",
	"try again",
	"temporarily unavailable",
	"service unavailable",
	"Internal server error",
	// Network/transport errors
	"connection refused",
	"connection reset",
	"EOF",
	"transport",
	"dial ",
	"i/o timeout",
	"broken pipe",
}

// IsRetryable returns true for transient errors that warrant a retry.
func (c *GhcpClient) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Never retry on context cancellation
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range ghcpRetryable {
		if strings.Contains(msg, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

// isSessionExpired reports whether an error indicates the server-side session
// is no longer valid and a fresh session should be created.
func isSessionExpired(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "session not found") ||
		strings.Contains(s, "session expired") ||
		strings.Contains(s, "disconnected") ||
		strings.Contains(s, "not connected")
}

// buildSDKTools converts Dagger LLMTools to Copilot SDK Tool definitions.
// The Handler calls tool.Call synchronously; the SDK manages the round-trip to
// the LLM inside SendAndWait so Dagger never sees raw tool-call/result pairs.
func buildSDKTools(tools []LLMTool) []copilot.Tool {
	if len(tools) == 0 {
		return nil
	}
	sdkTools := make([]copilot.Tool, 0, len(tools))
	for _, t := range tools {
		tool := t // capture loop var
		sdkTools = append(sdkTools, copilot.Tool{
			Name:           tool.Name,
			Description:    tool.Description,
			Parameters:     tool.Schema,
			SkipPermission: true,
			Handler: func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
				if tool.Call == nil {
					return copilot.ToolResult{
						Error:      fmt.Sprintf("tool %q has no handler", tool.Name),
						ResultType: "error",
					}, nil
				}

				// inv.Arguments is map[string]any from SDK JSON-RPC parsing.
				args, _ := inv.Arguments.(map[string]any)
				if args == nil {
					args = map[string]any{}
				}

				// inv.TraceContext carries W3C trace propagation from the CLI span.
				result, err := tool.Call(inv.TraceContext, args)
				if err != nil {
					return copilot.ToolResult{
						Error:      err.Error(),
						ResultType: "error",
					}, nil
				}

				switch v := result.(type) {
				case string:
					return copilot.ToolResult{TextResultForLLM: v, ResultType: "success"}, nil
				case []byte:
					return copilot.ToolResult{TextResultForLLM: string(v), ResultType: "success"}, nil
				default:
					b, err := json.Marshal(result)
					if err != nil {
						return copilot.ToolResult{TextResultForLLM: fmt.Sprintf("%v", result), ResultType: "success"}, nil
					}
					return copilot.ToolResult{TextResultForLLM: string(b), ResultType: "success"}, nil
				}
			},
		})
	}
	return sdkTools
}

// hashTools returns a stable fingerprint of the tool set by sorting tool names
// and joining them. A change in fingerprint triggers session recreation so the
// new tools are registered with the Copilot CLI.
func hashTools(tools []copilot.Tool) string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
