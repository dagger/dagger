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

---

## Configuration

Set these environment variables before running Dagger:

| Variable | Required | Description |
|----------|----------|-------------|
| `GITHUB_TOKEN` | ✅ Yes | GitHub personal access token or Actions token with Copilot access |
| `GITHUB_MODEL` | No | Model to use (default: `github-gpt-5`). See model names below. |
| `GITHUB_CLI_VERSION` | No | Copilot CLI binary version to embed in the sidecar (default: `1.0.10`) |
| `GITHUB_COPILOT_CLI_URL` | No | Override the tarball URL (for air-gapped or internal registry use) |

### Model names

The provider recognises these prefixes and strips them before passing to the SDK:

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

## Architecture

### Container-on-demand sidecar pattern

This provider uses the **[github.com/github/copilot-sdk/go](https://github.com/github/copilot-sdk) Go SDK (v0.2.0)** via a TCP JSON-RPC connection to a lightweight Dagger sidecar service:

```
Dagger Engine (GhcpClient)
  └── copilot.NewClient(&ClientOptions{CLIUrl: "host:3000"})
        └── TCP JSON-RPC → *dagger.Service (built on-demand)
              └── debian:bookworm-slim + copilot binary
                    └── copilot --headless --no-auto-update --port 3000
```

**No Node.js. No pre-published image.** The sidecar container is built at runtime by:

1. Fetching the Copilot CLI npm tarball via `dag.HTTP()` (content-addressable — downloaded once, cached forever)
2. Extracting the binary into a minimal `debian:bookworm-slim` base
3. Starting it as a Dagger service in headless mode on port 3000
4. Connecting the Go SDK via `CLIUrl` TCP option

The `GITHUB_TOKEN` is injected as an env var into the sidecar only — it is deliberately **not** passed to `ClientOptions.GitHubToken`, which would panic when combined with `CLIUrl`.

### Lazy initialisation

The sidecar is started on the **first `SendQuery` call**, not at client creation. `newGhcpClient` is synchronous and cheap; the Dagger service starts when there is an actual request to serve. `ensureConnected` is idempotent — subsequent calls return immediately.

### Session lifecycle

A single `copilot.Session` is created on first use and reused for the lifetime of the `GhcpClient`. The session is created with `Streaming: true` and `PermissionHandler.ApproveAll`.

---

## What works now (Phase 1)

| Feature | Status |
|---------|--------|
| Text responses (single-turn) | ✅ Working |
| Token usage tracking (OTel metrics) | ✅ Working — from SDK `assistant.usage` events |
| Transient error retry detection | ✅ Working — connection refused, EOF, transport errors |
| `GITHUB_CLI_VERSION` version pinning | ✅ Working |
| `GITHUB_COPILOT_CLI_URL` tarball override | ✅ Working |
| Multi-turn conversation history | 🔜 Phase 2 |
| Full tool / function calling | 🔜 Phase 3 |
| Streaming deltas | 🔜 Phase 4 |

---

## Limitations

### Multi-turn conversations (Phase 2)

Phase 1 sends only the **last user message** from the `history` slice as the prompt. All prior messages are discarded — the model has no context from previous turns.

Phase 2 will thread the full history by converting `ModelMessage` entries to SDK message format and replaying them into the session before each new user turn.

### Tool / function calling (Phase 3)

Tools passed to `SendQuery` are registered as SDK `Tool` definitions with **stub handlers** that return an error. The model receives tool schemas and may attempt to call them, but the stub handlers prevent actual execution.

Phase 3 will wire the handlers to `tool.Call(ctx, inv.Arguments)` and complete the Dagger tool execution loop.

### Streaming (Phase 4)

`SendAndWait` is used instead of `session.On + session.Send`. This buffers the complete response before returning. Phase 4 will switch to streaming deltas via `session.On(SessionEventTypeAssistantMessageDelta)`.

---

## Code structure

| File | Purpose |
|------|---------|
| `core/llm_github_copilot.go` | Provider implementation |
| `core/llm.go` | Router integration: `isGitHubModel`, `routeGitHubModel`, config loading |

### Key types

```go
type GhcpClient struct {
    endpoint   *LLMEndpoint     // token, model name
    cliVersion string           // CLI binary version for sidecar
    svc        *dagger.Service  // copilot CLI sidecar
    client     *copilot.Client  // SDK client connected via CLIUrl
    session    *copilot.Session // persistent session (multi-turn in Phase 2)
    mu         sync.Mutex       // protects svc, client, session
}
```

### Environment variable loading (in `LLMRouter`)

```go
GitHubToken      string  // GITHUB_TOKEN
GitHubModel      string  // GITHUB_MODEL
GitHubCliVersion string  // GITHUB_CLI_VERSION
```

