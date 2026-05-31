# LLM Proxy

## Goal

Add a Dagger primitive that lets a containerized process execute LLM tool calls
through Dagger, without giving that process direct access to host credentials,
provider APIs, or the engine internals.

The primitive should make this possible:

1. A user starts a normal `Container`.
2. The container runs an agent, coding assistant, MCP client, or other LLM-aware
   process.
3. That process talks to a local proxy endpoint injected by Dagger.
4. The proxy routes model requests through Dagger's `LLM` object and executes
   tool calls against an `Env`.
5. The result is still regular Dagger state: immutable objects, updated
   environment bindings, workspace changes, traces, cache metadata, and outputs.

In short: let arbitrary executables use Dagger's LLM runtime as their model and
tool backend.

## Motivation

Dagger already has an `LLM` type with prompts, model routing, tool calls, MCP
integration, and environments. That works well when the Dagger API is driving the
conversation.

Many useful agents invert that control flow. They expect to run as a process and
call an OpenAI-compatible API, an Anthropic-compatible API, or an MCP server.
Today, wiring those agents into Dagger usually means giving the container raw
provider credentials or writing adapter code around each agent.

An LLM proxy primitive would let Dagger own the sensitive and stateful parts:

- provider credentials stay in the engine/session;
- tool calls execute through Dagger's schema and `Env`;
- filesystem and service access remain explicit Dagger objects;
- traces show the model request, tool calls, object IDs, and resulting state;
- the same containerized agent can run locally, in CI, or in Dagger Cloud.

## Non-goals

- Do not make Dagger a general-purpose public LLM gateway.
- Do not expose undocumented engine endpoints to containers.
- Do not require every agent to be rewritten against Dagger's GraphQL API.
- Do not bypass the existing `LLM`, `Env`, secret, cache, trace, or permissions
  model.
- Do not guarantee compatibility with every provider feature in the first
  version.

## API sketch

The smallest useful shape is a service-like object that can be attached to a
container:

```graphql
type LLM {
  proxy(protocol: LLMProxyProtocol = OPENAI): LLMProxy!
}

type LLMProxy {
  endpoint: String!
  service: Service!
}

enum LLMProxyProtocol {
  OPENAI
  ANTHROPIC
  MCP
}
```

Example shell flow:

```shell
dagger <<'EOF'
src=$(host | directory ".")
environment=$(env |
  with-directory-input "src" $src "source tree to inspect" |
  with-string-output "summary" "short summary of the result")
proxy=$(llm --model claude |
  with-env $environment |
  proxy)

container |
  from node:22 |
  with-service-binding "llm-proxy" $proxy.service |
  with-env-variable "OPENAI_BASE_URL" $proxy.endpoint |
  with-env-variable "OPENAI_API_KEY" "dagger" |
  with-directory /work $src |
  with-workdir /work |
  with-exec ["my-agent", "--task", "summarize the project"] |
  sync
EOF
```

The exact API names can change, but the important property is that Dagger
creates both the network endpoint and the backing `LLM` state.

## Semantics

The proxy is scoped to an `LLM` value.

- `llm.withEnv(env).proxy()` exposes tools from `env`.
- `llm.withModel(model).proxy()` routes requests to that model.
- `llm.withSystemPrompt(prompt).proxy()` includes those prompts.
- `llm.withBlockedFunction(typeName, function).proxy()` hides blocked tools.
- `llm.withStaticTools().proxy()` can force a static tool list for clients that
  cannot handle dynamic tool discovery.

Requests through the proxy update the same logical LLM conversation. Tool calls
are evaluated by Dagger, not by the container process. If a tool returns an
`Env`, that environment becomes the current environment for subsequent tool
calls. If a tool returns a `Changeset`, the changes are applied to the
environment workspace using the existing LLM tool semantics.

The proxy should be deterministic about ownership: the container can ask for
model completions, but Dagger owns the tool registry, object IDs, credential
resolution, tracing, and persistence of state.

## Protocols

### OpenAI-compatible HTTP

First target because most existing agents can use it with only `OPENAI_BASE_URL`
and `OPENAI_API_KEY`.

Minimum endpoints:

- `POST /v1/chat/completions`
- `POST /v1/responses`, if needed by common clients
- `GET /v1/models`

The proxy translates provider-agnostic requests into Dagger `LLM` calls, then
translates tool-call messages back into the requested wire format.

### Anthropic-compatible HTTP

Useful for agents that rely on Anthropic's messages API directly.

Minimum endpoint:

- `POST /v1/messages`

This can be implemented after the OpenAI-compatible path unless a first user
requires it.

### MCP

The existing `LLM.__mcp` path exposes a Dagger-backed MCP server for model tool
use. A proxy primitive should make the inverse ergonomic too: an external agent
can connect to an MCP endpoint backed by a Dagger `Env`.

This may be a separate `env.mcp()` or `llm.proxy(protocol: MCP)` API depending
on whether the endpoint needs model routing or only tool execution.

## Security model

The proxy must not forward provider credentials into the container. The
container receives only an endpoint and, if a client requires it, a placeholder
API key accepted by the proxy.

Access is limited to the attached Dagger objects:

- tools come from the `Env` and any explicitly attached MCP servers;
- host filesystem access only exists through attached `Directory` or `File`
  objects;
- service access only exists through attached `Service` objects;
- secrets remain Dagger `Secret` values and are never serialized as plain text
  in proxy configuration.

The proxy should be session-scoped by default. A container from another session
must not be able to reuse the endpoint.

## Observability

Each proxied request should produce trace spans that connect:

- the container exec that initiated the request;
- the proxy request and selected model;
- provider request/response metadata;
- tool calls and their Dagger selectors;
- token usage;
- resulting object IDs and environment changes.

This is one of the main reasons to keep the proxy as a Dagger primitive instead
of documenting an internal endpoint.

## Open questions

- Should the proxy be an `LLM` method, an `Env` method, or a new top-level
  `llmProxy` constructor?
- Should proxy requests mutate one shared LLM history, or should each HTTP
  conversation be keyed by provider conversation IDs?
- How should streaming map to traces and tool execution boundaries?
- What is the right default for parallel tool calls?
- How should `maxAPICalls` count proxied model calls and tool continuations?
- Should the API expose provider-specific compatibility modes, or infer them
  from the incoming path?
- What should be cached: provider responses, tool results, both, or neither?

## First milestone

Implement an experimental OpenAI-compatible proxy for containers:

1. Add an `LLM.proxy()` API that returns a `Service` and endpoint URL.
2. Support `POST /v1/chat/completions` without streaming.
3. Support model tool calls through the existing `LLM` and `Env` machinery.
4. Keep provider credentials entirely outside the container.
5. Emit trace spans for proxy requests, provider calls, tool calls, and token
   usage.
6. Add an integration test that runs a containerized OpenAI-compatible client
   against the proxy and saves an `Env` output.

That milestone proves the primitive without committing to every provider or
streaming behavior.
