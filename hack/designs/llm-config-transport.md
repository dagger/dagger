# LLM configuration: one document from the client, not 31 secrets

Status: **implemented**. Hard cut-over, no compatibility shim: an old CLI
against a new engine gets no LLM configuration, and a new CLI against an old
engine sends a field the engine ignores. See "Contracts that change" at the
bottom.

## The problem

LLM configuration reached the engine through two systems glued together by
an env-var flattening step:

```text
~/.config/dagger/config.toml [llm]      $ANTHROPIC_API_KEY, ...     ./.env
        │ llmconfig.Load()                     │                       │
        ▼                                      │                       │
applyLLMConfigEnv()  ──os.Setenv(~25)──►  process env ◄────────────────┘ (file://.env)
(cobra.OnInitialize, EVERY command)            │
                                               │ secret(uri:"env://X"){plaintext} × ~28
                                               ▼
                                   LLMRouter.loadConfig (core/llm.go)
```

- The CLI's `applyLLMConfigEnv` was a `switch` encoding the TOML `Provider`
  struct into conventional env var names, with ~90 lines of
  `llmEnvExports` bookkeeping to tell "we exported this" from "the user did".
- The engine's `loadConfig` ran ~28 `secret(uri:"env://X"){plaintext}`
  selections per router load. A `secret()` without `cacheKey` resolves the
  plaintext twice (argon2 handle, then `.plaintext`): ~56 client RPCs + 27
  argon2 hashes per load, doubled for a nested client (main + parent). That
  ran on every `llm()` without a model, every `SmallModelRoute`, and the
  first `Endpoint()` per LLM node per session.
- 27 of the 31 variables were non-secret routing config (model names, base
  URLs, reasoning effort) going through the secrets API because that was the
  only channel.
- OAuth freshness was a side-channel: `secretprovider.RegisterEnvRefresher`
  hooked every `env://` resolution, matched two variable names, and the
  engine learned expiry through a `_EXPIRES_AT` sibling-variable convention.
- `llmconfig.MergeEnvVars` and `DAGGER_MODEL` were dead code.

## The design

Two kinds of configuration, two channels.

### Routing config rides in ClientMetadata

`engine.LLMConfig` (engine/llm_config.go) is the wire type. The CLI assembles
it **once per process** from, lowest to highest precedence:

1. `~/.config/dagger/config.toml` `[llm]` (or `$DAGGER_CONFIG`), written by
   `/setup` in `dagger agent` (also `.setup` in the shell and automatic
   setup when `dagger agent` has no provider configured);
2. the process environment (`ANTHROPIC_API_KEY`, `OPENAI_MODEL`, ...);
3. `./.env` in the command's working directory.

(That order matches the previous behaviour: `.env` was consulted before
`env://`, and explicit env vars always beat the config file.)

It travels in `ClientMetadata.LLMConfig` (`llm_config` in the
`X-Dagger-Client-Metadata` header), next to `UserConfigPath`, `Workspace` and
`CloudAuth`. The engine's `loadLLMRouter` reads it off the client metadata
with a direct `provider → router fields` mapping: **zero RPCs** at routing
time.

Provider names are one vocabulary end to end: the config file's
`[llm.providers.<name>]` keys, the wire, `llm(provider:)` and `LLM.provider`
all use `anthropic`, `openai`, `openai-codex`, `openrouter`, `google`,
`github-copilot`, `local`. OpenRouter is a first-class engine provider (OpenAI-compatible
client, its own credential, default base URL `https://openrouter.ai/api/v1`),
so an `openai` key and an `openrouter` key coexist; its model names carry
the upstream vendor as a prefix (`anthropic/claude-…`), so it is only ever
selected explicitly — by provider or as the configured default — never
inferred from a name. The old `gemini` alias for `google` is gone.

`github-copilot` uses the user's own Copilot subscription with a borrowed
public Copilot OAuth client and the allow-listed `vscode-chat` integration
identity; `User-Agent` and `Editor-Version` identify Dagger. It is selected
by `llm(provider: "github-copilot")` or a default route, never inferred from
a model name and with no model-name prefix. Setup's default, `/model`'s
saved default, `default_provider` in the file and `COPILOT_MODEL` can pin
that route. A Copilot login alone never overrides another provider's API
key during automatic selection. Usage reports token counts without cost.

Layering is unchanged: the session's main client seeds the router, the
calling (non-module) client overlays it field by field, and whichever client
supplied the local base URL owns the tunnel. The Anthropic rule that an API
key and an OAuth token are alternatives for one slot (a later load that
supplies one clears the other) is preserved in `LLMProviderConfig.Merge`.

### Credentials are secret URIs, resolved lazily

`LLMProviderConfig.APIKey`, `.AuthToken` and `.AuthTokenExpiresAt` carry
**secret URIs**, never values. The engine resolves them against the client
that supplied them (the `bindClient` property from the OAuth lifecycle work),
and only for the provider actually routed, through the existing
`CredentialSource` (caching to expiry, 401 invalidation, rejected-value
fingerprinting).

The CLI emits:

| source | URI |
| --- | --- |
| process env `ANTHROPIC_API_KEY=sk-...` | `env://ANTHROPIC_API_KEY` |
| any source whose value is itself a URI (`op://…`, `vault://…`) | the value, verbatim |
| literal stored in config.toml, or a literal in `.env` | `llmconfig://<provider>/api_key` |
| OAuth login stored in config.toml | `llmconfig://<provider>/auth_token` + `llmconfig://<provider>/auth_token_expires_at` |
| `COPILOT_GITHUB_TOKEN` (process env or `.env`) | `llmconfig://github-copilot/auth_token` + `llmconfig://github-copilot/auth_token_expires_at`; exchanged by the resolver |
| explicit `ANTHROPIC_AUTH_TOKEN` env | `env://ANTHROPIC_AUTH_TOKEN` (+ `env://…_EXPIRES_AT` only if set) |

`llmconfig://` is a new `secretprovider` scheme. The engine binary knows the
scheme (so `secret(uri:)` accepts it) but only the CLI registers a resolver
(`secretprovider.RegisterLLMConfigResolver`). The resolver re-reads the
config file under the cross-process lock at every resolution, refreshes an
OAuth token that is due (or that the engine reports as rejected, via the
`RejectedSecretValue` fingerprint metadata), and persists the rotated token —
exactly what the env refresher hook did, minus the `os.Setenv` round trip.
Because an explicitly exported API bearer is sent as `env://…`, the `llmconfig://`
resolver is never consulted for it, so a rejected user-supplied bearer can't
rotate an unrelated subscription login.

For the other subscription providers, the engine reads the token URI first
and the expiry URI second within one resolution, as before: the token read
is what may rotate both. Copilot requires an atomic snapshot, described below.

Resolution bypasses dagql: `core.resolveClientSecret` builds a
`core.Secret{URIVal, SourceClientID}` and calls `Plaintext`, i.e. one
`GetSecret` RPC on the supplying client's session attachables. No argon2
handle, no `PerCallInput` node.

### Copilot refresh carries the API origin with the bearer

`/setup` stores the device flow's GitHub token as `refresh_token`, with
`auth_type = "oauth"`, under `[llm.providers.github-copilot]`. This GitHub
token is not the Copilot API bearer. The resolver's refresh exchanges it
for a short-lived Copilot token (about 30 minutes), storing that token as
`auth_token`, its true expiry, and the exchange's `endpoints.api` as
`base_url`. Login and every stored-login refresh persist all three. An
exchange without `endpoints.api` uses `https://api.githubcopilot.com`.

For CI, `COPILOT_GITHUB_TOKEN` supplies the GitHub token to exchange instead.
It wins over a stored login: the resolver caches its exchanged token,
expiry and API origin together in memory and does not exchange the stored
GitHub token. `COPILOT_MODEL`, `COPILOT_SMALL_MODEL` and
`COPILOT_REASONING_EFFORT` set the provider's corresponding routing fields.
`GH_TOKEN`, `GITHUB_TOKEN` and credentials from other tools are not read.

Client metadata still carries `AuthToken` as
`llmconfig://github-copilot/auth_token`, not a plaintext bearer. When building
the Copilot endpoint, the engine's Copilot credential reloader resolves its
companion `llmconfig://github-copilot/credential` through the same secret RPC.
The CLI serves one JSON `engine.CopilotCredential` snapshot with `token`,
`expires_at` (true expiry) and `base_url` (the exchange's `endpoints.api`).
Separate token and base-URL reads could race a refresh by another CLI and
pair a bearer with the wrong endpoint; this single read keeps them together.
The GitHub token stays on the CLI side.

`CredentialSource` caches and invalidates the complete tuple. Initial
endpoint construction uses the snapshot's origin, covering both an expired
stored login with a stale `base_url` and an env-only first exchange with no
URL yet. On every HTTP attempt, `credentialTransport` applies the bearer
and swaps the request URL's scheme and host and the HTTP `Host` to the
snapshot's origin, preserving the OpenAI client's path and query. This also
covers SDK retries and later requests on a memoized endpoint after a
mid-session refresh changes `endpoints.api`.

The resolver re-exchanges the GitHub token when the Copilot token is due or
when the engine sends its rejected-value fingerprint, even before expiry.
A 401/403 from the exchange surfaces `ErrOAuthReauthenticationRequired`
and tells the user to run `/setup` in `dagger agent`. The CLI's device flow,
token exchange and engine API requests share `engine.CopilotHeaders()`.

### What the engine does with it

`LLMRouter` is now keyed by provider: `Providers map[LLMProvider]*llmProviderRoute`,
each holding the routing fields and the credential references. `Route()`
stays pure (it is called from `DefaultLLMRoute` just to name the provider);
`Endpoint()` resolves the routed provider's API key once, when the endpoint
is built, and hands OAuth tokens to a `CredentialSource` as before.

`DefaultRoute()` honours the config's explicit `default_provider` /
`default_model` first — fixing a latent bug where the file's Anthropic
default lost to an exported `OPENAI_MODEL` because the engine chose by
provider priority — and falls back to configured models in provider priority
(openai, openai-codex, openrouter, anthropic, google, local), then by
configured credential when no default is set. Copilot's saved per-provider
model does not select the default route; it is used when Copilot is selected
explicitly or is the credential fallback. Model environment variables
can set an explicit default during CLI assembly;
`COPILOT_MODEL` comes after Google's model variables. Copilot default routes
stay pinned to `github-copilot` even for `claude-*`, `gpt-*` and `gemini-*`
models, whose names overlap the other vendors. A sole Copilot credential
can supply the fallback default, but API-key providers precede it.

## Contracts that change

- **Old CLI ↔ new engine: no LLM configuration.** The engine no longer probes
  `env://` variables; a CLI that still exports them configures nothing.
  There is no transition path short of pinning versions.
- **New CLI ↔ old engine: no LLM configuration either**, for the mirror
  reason. Again version pinning only.
- **`file://.env` is no longer read by the engine.** The CLI reads `./.env`
  itself (same path, same keys, same precedence). A client that is not the
  `dagger` binary (a module's nested client, an SDK talking to `dagger
  session`) never read `.env` through its own cwd anyway — the session's
  CLI did, and still does.
- **`DAGGER_MODEL` is gone.** It only ever existed in dead code.
- **`secretprovider.RegisterEnvRefresher` / `EnvRefresher` are gone.** The
  `llmconfig://` resolver replaces them. `ContextWithRejectedEnvValue` /
  `RejectedEnvValue` are renamed `…SecretValue`; the gRPC metadata key is
  unchanged.
- **`ANTHROPIC_AUTH_TOKEN_EXPIRES_AT` / `OPENAI_CODEX_AUTH_TOKEN_EXPIRES_AT`**
  are still honoured when the user exports them next to an explicit token,
  but the CLI no longer exports them itself.
- **Nested clients.** A nested `dagger` CLI (privileged nesting) assembles
  its own `LLMConfig` from its container's env/`.env`/config and sends it in
  its own metadata; the engine overlays it on the main client's. Credentials
  the nested client did not supply resolve against the main client, so the
  container still never sees the keys. `TestNestedClientInheritsSessionConfig`
  pins this.
- **Scale-out.** `LLMConfig` is forwarded with the scale-out client's
  metadata like `Workspace` and `CloudAuth`. `env://` URIs resolve against
  the forwarding engine's environment; `llmconfig://` URIs cannot resolve
  there (no CLI resolver), same as before when the engine had no env.
- **`openrouter` is a provider of its own.** Previously it was folded into
  the `openai` slot, so only one of the two could be configured at a time
  and `llm(provider: "openrouter")` was an error. `LLM.provider` now reports
  `openrouter` for such conversations, and `OPENROUTER_API_KEY` /
  `OPENROUTER_BASE_URL` / `OPENROUTER_MODEL` / `OPENROUTER_SMALL_MODEL` /
  `OPENROUTER_REASONING_EFFORT` configure it from the environment. A CI
  setup that pointed `OPENAI_BASE_URL` at OpenRouter keeps working as the
  `openai` provider.
- **`[llm.providers.gemini]` is no longer recognized.** The former
  `dagger llm setup` command always wrote `google`; a hand-edited `gemini`
  section must be renamed. Setup is now `/setup` in `dagger agent`.
- **Non-credential values that are secret URIs** (`OPENAI_BASE_URL=op://…`)
  were resolved by the engine's old loader as a side effect of every value
  going through `loadSecret`. They now travel as plain strings. Resolving
  them at startup would run `op` and friends on every command.
- **An Anthropic API key in the environment next to an OAuth login in the
  file**: the old engine received both and the OAuth token won. Now the
  higher-precedence source (the env key) wins and the token is dropped —
  the `Merge` rule, applied uniformly.
- **`KEY=` in `.env`** counts as unset. It used to shadow the process env
  with an empty value.
- **A literal `*_AUTH_TOKEN` in `.env`** is served as read and never
  refreshed, so a rejected `.env` bearer can't rotate the file's login.
- **A malformed `OPENAI_DISABLE_STREAMING`** fails every command that
  connects to the engine (it used to fail at LLM routing time). An
  unreadable config file or `.env` is warned about and skipped.
- The file's `default_provider`/`default_model` are not sent when that
  provider is disabled.

## Invariants kept from the OAuth lifecycle work

- Plain API keys and CI are exactly unaffected downstream of routing:
  `newCredentialTransport` returns the base transport unchanged when there is
  no OAuth source.
- Nothing credential-related enters a dagql ID or content digest; the
  recording route has no credential.
- A credential resolves against the client that supplied it, never the
  client making the request.
- Bearer tokens do not reach telemetry.

## Verification

```console
go build ./...
go test ./core/ ./core/schema/ ./engine/... ./internal/cmd/dagger/... -count=1
dagger call engine-dev test --pkg ./core/integration --run 'TestLLM/(TestNestedClientInheritsSessionConfig|TestDefaultModelPinnedInTrace|TestSmallModelPinnedInTrace)'
dagger call engine-dev test --pkg ./core/integration --run 'TestCrossSession/.*LLM'
```
