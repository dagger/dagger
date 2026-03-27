# GitHub Copilot (GHCP) LLM Provider

## Overview

This document describes the GitHub Copilot LLM provider for Dagger (`GhcpClient`), how to use it, its current limitations, and the roadmap for improvement.

The provider allows Dagger's LLM system to use GitHub Copilot as a model backend — enabling access to models like GPT-4.1, Claude Sonnet, and others that GitHub Copilot exposes — without requiring direct API keys to those underlying providers.

---

## Why GitHub Copilot as an LLM provider?

GitHub Copilot acts as a **unified gateway** to multiple frontier models (OpenAI, Anthropic, Google, etc.) under a single GitHub token. This has practical advantages:

- **One credential** — a `GITHUB_TOKEN` with Copilot access, rather than separate API keys per provider
- **Enterprise licensing** — organizations with GitHub Copilot Enterprise can route Dagger LLM calls through their existing seat licenses
- **Model flexibility** — switch between GPT-4.1, Claude Sonnet, Gemini, etc. by changing the model name, without managing multiple API accounts

The tradeoff is the [CLI-based execution model](#the-cli-container-requirement) described below.

---

## Configuration

Set these environment variables before running Dagger:

| Variable | Required | Description |
|----------|----------|-------------|
| `GITHUB_TOKEN` | ✅ Yes | GitHub personal access token or GitHub Actions token with Copilot access |
| `GITHUB_CLI_VERSION` | ✅ Yes | Version of `@github/copilot` npm package to install (e.g. `1.0.0`) |
| `GITHUB_MODEL` | No | Model to use (default: `github-gpt-5`). See model names below. |

### Model names

The provider recognises these prefixes and strips them before passing to the CLI:

| Prefix | Example |
|--------|---------|
| `github-` | `github-gpt-5`, `github-claude-sonnet-4-5` |
| `github/` | `github/gpt-5` |
| `gh-` | `gh-gpt-5` |
| `gh/` | `gh/gpt-5` |
| `ghcp-` | `ghcp-gpt-5` |
| `ghcp/` | `ghcp/gpt-5` |

The alias `"github"` resolves to `"github-gpt-5"`.

---

## How it works

### The CLI-container requirement

**There is no official Go SDK for GitHub Copilot that calls the API directly.** What exists today is a set of language-specific SDKs (TypeScript, Python, etc.) that are thin wrappers around the **GitHub Copilot CLI** (`@github/copilot` npm package). The CLI itself communicates with the Copilot API endpoint.

As a result, this provider cannot make direct HTTP calls. Instead, it:

1. **Spins up a Dagger container** based on `node:24-bookworm-slim`
2. **Installs the GHCP CLI** via `npm install -g @github/copilot@{version}` inside the container
3. **Executes each query** by running `copilot --model {model} --prompt {content} --stream off --continue`
4. **Captures stdout** for the response and **stderr** for token usage metadata

This is unconventional — every LLM call shells out to a CLI inside a container rather than making an SDK/HTTP call. It works, but carries implications for latency and dependency management (see [Limitations](#limitations-and-gaps)).

### Session and state caching

The GHCP CLI stores session state in `/root/.copilot`. The provider mounts a **Dagger cache volume** keyed on the first 8 characters of the token (`copilot-session-{token[:8]}`) at that path, so session state persists across queries within a Dagger run.

The `--continue` flag is passed to the CLI to enable continuation of an existing session.

> **Note:** This caching is a best-effort approach. Multi-turn conversation history is not truly supported — see [Multi-turn conversations](#multi-turn-conversations) below.

### Token usage parsing

The GHCP CLI emits token usage on stderr in a format like:

```
claude-sonnet-4.5    7.5k input, 52 output, 3.6k cache read, 3.7k cache write (Est. 1 Premium request)
```

The provider parses this with a regex and emits the values as OpenTelemetry metrics:
- `telemetry.LLMInputTokens`
- `telemetry.LLMOutputTokens`
- `telemetry.LLMInputTokensCacheReads`

The `k` multiplier is handled (e.g. `3.5k` → `3500`). Cache write tokens are parsed but not currently recorded as a separate OTel metric.

---

## Limitations and gaps

### The CLI-container requirement

The most significant architectural constraint. Every query requires:
- A running container with Node.js 24+
- npm install of `@github/copilot` at a specific version
- CLI execution overhead per query

This adds latency compared to SDK-based providers (Anthropic, OpenAI) and introduces a hard dependency on the npm registry and a specific CLI version. The `GITHUB_CLI_VERSION` variable must be pinned to a known-good version.

**Roadmap:** Migrate to direct API calls once the GitHub Copilot API is publicly documented or a first-class Go SDK is available. See [Future work](#future-work).

### Multi-turn conversations

The GHCP CLI only accepts `--prompt` (a single string). The provider passes only the **last message** from the `history` slice:

```go
prompt := history[len(history)-1]
```

All prior messages in the conversation are discarded. Although the CLI stores state as a `.jsonl` file and the `--continue` flag is used, it does not replay or reference prior messages in the session — it only continues the CLI session context, not the LLM conversation history.

**Impact:** Dagger LLM pipelines that rely on multi-turn context will not work correctly with this provider today.

### Tool / function calling

The `tools []LLMTool` parameter is **completely ignored**. The provider always returns an empty `toolCalls` slice:

```go
var toolCalls []LLMToolCall  // always empty
```

The GHCP CLI has no native support for function/tool calling schemas. This means Dagger's tool-use features (e.g. calling container methods from within an LLM loop) are not available with the GHCP provider.

**Impact:** Any Dagger pipeline that uses `WithTool` or expects the LLM to call Dagger GraphQL methods will fail silently — the model will respond with text but never invoke tools.

### Retry logic

`IsRetryable()` always returns `false`. There is no backoff or retry on transient failures (rate limits, network errors, CLI crashes).

### Streaming

The CLI is invoked with `--stream off`. Streaming output is not parsed or forwarded. All responses are buffered.

---

## Future work

### 1. Migrate to GitHub Copilot SDK

The language-specific GitHub Copilot SDKs (TypeScript: `@github/copilot-sdk`, Python: `copilot-sdk`) are thin wrappers around the CLI — they are **not** direct API clients. However, they do expose a slightly higher-level interface and may be more stable than shelling out to the CLI directly.

A more impactful improvement would be to call the **GitHub Copilot API endpoint directly** once:
- The API is publicly documented (currently undocumented/private)
- A Go client library is available, or the REST contract is stable enough to implement manually

This would eliminate the container/npm dependency entirely and align the provider with how OpenAI, Anthropic, and Google providers work.

> **Note:** When evaluating "GitHub Copilot SDK" options, be aware that today's SDKs wrap the CLI, not the API. True API-level SDKs do not yet exist as of this writing.

### 2. Multi-turn conversation history

Options to explore:
- Accumulate prior messages into a system prompt prefix (prompt injection)
- Use the GHCP CLI's `.jsonl` history format if/when documented
- Wait for API-level access which natively supports conversation history

### 3. Tool calling

Options to explore:
- Prompt injection: describe available tools in the system prompt, parse structured responses
- Wait for API-level access with native function calling support

### 4. Retry / backoff

Implement `IsRetryable()` with pattern matching on error strings (rate limit, network timeout). Add exponential backoff.

### 5. Streaming

Replace `--stream off` with `--stream on` and parse chunked stdout. Token metadata may arrive at the end of the stream rather than after completion.

---

## Code structure

| File | Purpose |
|------|---------|
| `core/llm_github_copilot.go` | Provider implementation: `GhcpClient`, `GhcpClientContainer`, `StripGitHubModelPrefix`, `parseCopilotTokenMetadata` |
| `core/llm.go` | Router integration: `isGitHubModel`, `routeGitHubModel`, config loading, model alias |

### Key types

```go
// Provider client — satisfies LLMClient interface
type GhcpClient struct {
    client   *dagger.Container  // Node.js container with GHCP CLI installed
    endpoint *LLMEndpoint       // Config: token, model
}

// LLMClient interface methods implemented
func (c *GhcpClient) SendQuery(ctx, history, tools) (*LLMResponse, error)
func (c *GhcpClient) IsRetryable(err error) bool
```

### Environment variable loading (in `LLMRouter`)

```go
GitHubToken      string  // GITHUB_TOKEN
GitHubModel      string  // GITHUB_MODEL
GitHubCliVersion string  // GITHUB_CLI_VERSION
```
