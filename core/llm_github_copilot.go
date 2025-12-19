package core

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/dag"
	"dagger.io/dagger/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type GhcpClient struct {
	client   *dagger.Container
	endpoint *LLMEndpoint
}

func newGhcpClient(endpoint *LLMEndpoint, cliVersion string) *GhcpClient {
	ctx := context.Background()

	// Since there is no official Go SDK for GitHub Copilot at the moment, we will use the GitHub Copilot CLI via a Dagger container.
	var container = GhcpClientContainer(ctx, endpoint.Key, cliVersion)

	return &GhcpClient{
		client:   container,
		endpoint: endpoint,
	}
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

// Strip Model Prefix from GitHub Model Names
// E.g "github-gpt-5" -> "gpt-5"
// Since GitHub uses various models we want to avoid model name collisions with other providers
// we default to "github-gpt-5" for now but will update this in future to allow the GHCP CLI to fall back to its own default model
func StripGitHubModelPrefix(model string) string {
	for _, prefix := range gitHubModelPrefixes {
		if strings.HasPrefix(model, prefix) {
			return strings.TrimPrefix(model, prefix)
		}
	}
	return model
}

func GhcpClientContainer(
	ctx context.Context,
	token string,
	cliVersion string,
) *dagger.Container {
	return dag.Container().
		From("node:24-bookworm-slim").
		WithExec([]string{"npm", "install", "-g", fmt.Sprintf("@github/copilot@%s", cliVersion)}).
		WithEnvVariable("GITHUB_TOKEN", token).
		WithWorkdir("/workspace")
}

// Satisfy the LLMClient interface with SendQuery and IsRetryable
func (c *GhcpClient) SendQuery(ctx context.Context, history []*ModelMessage, tools []LLMTool) (_ *LLMResponse, rerr error) {

	var copilotModel = StripGitHubModelPrefix(c.endpoint.Model)
	// instrument the call with telemetry
	// todo: moving to setup function to clean this up
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

	inputTokens, err := m.Int64Gauge(telemetry.LLMInputTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to get inputTokens gauge: %w", err)
	}

	inputTokensCacheReads, err := m.Int64Gauge(telemetry.LLMInputTokensCacheReads)
	if err != nil {
		return nil, fmt.Errorf("failed to get inputTokensCacheReads gauge: %w", err)
	}

	outputTokens, err := m.Int64Gauge(telemetry.LLMOutputTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to get outputTokens gauge: %w", err)
	}

	// Ensure there is at least one message in history
	if len(history) == 0 {
		return nil, fmt.Errorf("prompt/chat history cannot be empty - run with-prompt to add a prompt/message")
	}

	// Get the last message as the prompt
	// This is presumed to be the user prompt at the moment
	// Since GitHub Copilot CLI currently only supports single prompt input when running from a command line (--prompt)
	// Also note that GHCP CLI does not currently support chat history or multi-turn conversations, even though it stores state/history as a jsonl file
	prompt := history[len(history)-1]
	if prompt.Role != "user" {
		return nil, fmt.Errorf("the last message in history must be from the user")
	}

	var copilot = c.client.WithExec([]string{
		"copilot",
		"--model", copilotModel,
		"--prompt", prompt.Content,
		"--stream", "off",
	})

	// We aren't implement tool calls for GHCP at the moment
	var toolCalls []LLMToolCall

	content, err := copilot.Stdout(ctx)
	if err != nil {
		return nil, err
	}

	ghcpResponseMetadata, err := copilot.Stderr(ctx)
	if err != nil {
		return nil, err
	}

	llmTokenUsage := parseCopilotTokenMetadata(ghcpResponseMetadata)

	// Record metrics for token usage with attributes in OTel
	inputTokens.Record(ctx, llmTokenUsage.InputTokens, metric.WithAttributes(attrs...))
	outputTokens.Record(ctx, llmTokenUsage.OutputTokens, metric.WithAttributes(attrs...))
	inputTokensCacheReads.Record(ctx, llmTokenUsage.CachedTokenReads, metric.WithAttributes(attrs...))

	return &LLMResponse{
		Content:    content,
		ToolCalls:  toolCalls,
		TokenUsage: llmTokenUsage,
	}, nil
}

// We're not implementing any retries at the moment
func (c *GhcpClient) IsRetryable(err error) bool {
	// There is no auto retry at GHCP CLI That I know of at the moment
	return false
}

// parseCopilotTokenMetadata parses the stderr output (GHCP CLI Meatdata) from GitHub Copilot CLI to extract token usage information
func parseCopilotTokenMetadata(copilotclimetadata string) LLMTokenUsage {
	var tokenUsage LLMTokenUsage

	// Parse the usage line that contains model-specific token information
	// Example: "claude-sonnet-4.5    7.5k input, 52 output, 3.6k cache read, 3.7k cache write (Est. 1 Premium request)"
	// Note: cache write is optional and may not always be present

	// Look for the pattern: #k input, #k output, #k cache read, and optionally #k cache write
	re := regexp.MustCompile(`(\d+(?:\.\d+)?)(k?)\s+input,\s*(\d+(?:\.\d+)?)(k?)\s+output,\s*(\d+(?:\.\d+)?)(k?)\s+cache read(?:,\s*(\d+(?:\.\d+)?)(k?)\s+cache write)?`)
	matches := re.FindStringSubmatch(copilotclimetadata)

	if len(matches) > 7 {
		tokenUsage.InputTokens = parseTokenValue(matches[1], matches[2])
		tokenUsage.OutputTokens = parseTokenValue(matches[3], matches[4])
		tokenUsage.CachedTokenReads = parseTokenValue(matches[5], matches[6])

		// Note: cache write tokens are optional and may not always be present
		tokenUsage.CachedTokenWrites = parseTokenValue(matches[7], matches[8])

		tokenUsage.TotalTokens = tokenUsage.InputTokens + tokenUsage.OutputTokens
	}

	return tokenUsage
}

// parseTokenValue converts a string token value from GitHub Copilot CLI Metadata with an optional 'k' multiplier into an int64
func parseTokenValue(valueStr string, multiplierStr string) int64 {
	// Convert string to a float first to handle decimal values
	if inputVal, err := strconv.ParseFloat(valueStr, 64); err == nil {
		//  Apply multiplier if 'k' is present (e.g 3.5k = 3500)
		if strings.ToLower(multiplierStr) == "k" {
			inputVal *= 1000
		}
		return int64(inputVal)
	}
	return int64(0)
}
