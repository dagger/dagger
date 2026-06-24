# LLM Proxy

## Goal

Add a Dagger primitive that lets a containerized process send LLM traffic through
a Dagger-managed proxy, while Dagger emits best-effort traces of the model
session.

The first version is a pass-through proxy, not an active LLM runtime. Its core
contract is:

1. Forward model traffic without dropping, corrupting, or re-encoding the LLM
   session.
2. Keep provider credentials outside the container.
3. Observe copied traffic on a best-effort basis and emit useful spans when the
   proxy understands what is flowing through it.
4. Degrade observability, not correctness, when the proxy cannot parse a request,
   response, streaming chunk, or provider-specific field.

In short: make Dagger a safe, observable transport for arbitrary LLM-aware
processes before making it an active participant in the conversation.

## Motivation

Many agents, coding assistants, and MCP clients already know how to talk to an
OpenAI-compatible, Anthropic-compatible, or provider-specific API. Dagger users
should be able to run those tools in containers without copying provider
credentials into the container and without losing the Dagger trace as the place
to understand what happened.

The important constraint is that compatibility matters more than perfect
understanding. If an agent sends fields the proxy does not recognize, uses
streaming, or depends on provider-specific behavior, the proxy should still
forward the session intact. Missing spans are acceptable. Broken LLM sessions are
not.

## Non-goals

- Do not parse and re-encode normal provider requests as the forwarding path.
- Do not execute Dagger `Env` tools from the proxy in the first version.
- Do not return final `LLM` or `Env` state from a proxied container execution in
  the first version.
- Do not provide a general public LLM gateway.
- Do not expose undocumented engine endpoints to containers.
- Do not guarantee complete telemetry for every provider feature.

## Design principle

The proxy has two paths:

### Data path

The data path is authoritative. It forwards the incoming request and upstream
response as streams.

The proxy may perform minimal transport-level work:

- select the upstream provider endpoint;
- replace or inject provider authentication;
- normalize hop-by-hop HTTP details required by the proxy implementation;
- enforce session scoping and policy;
- copy request and response bytes to the observer.

It should not depend on JSON decoding in order to forward a request. If the
observer fails, the data path must continue.

### Observation path

The observation path receives a copy of the traffic and tries to understand it.
It can emit spans for:

- provider, model, endpoint, and request IDs;
- request start and response completion;
- streaming chunks when they are understandable;
- assistant text deltas;
- tool-call declarations and results;
- token usage when provided by the upstream API;
- errors reported by the provider or client connection.

Observation is explicitly lossy. Parser errors should produce a debug event or a
partial span, then stop parsing that message. They must not change the forwarded
traffic.

## API sketch

The exact API shape is open, but the first version should expose a service that
can be bound into a container:

```graphql
type Query {
  llmProxy(protocol: LLMProxyProtocol = OPENAI): LLMProxy!
}

type LLMProxy {
  service: Service!
  endpoint: String!
}

enum LLMProxyProtocol {
  OPENAI
  ANTHROPIC
}
```

Example shell flow:

```shell
dagger <<'EOF'
proxy=$(llm-proxy --protocol openai)

container |
  from node:22 |
  with-service-binding "llm-proxy" $proxy.service |
  with-env-variable "OPENAI_BASE_URL" "http://llm-proxy:8080/v1" |
  with-env-variable "OPENAI_API_KEY" "dagger" |
  with-directory /work $(host | directory ".") |
  with-workdir /work |
  with-exec ["my-agent", "--task", "summarize the project"] |
  sync
EOF
```

The placeholder API key is only for clients that require an API key to be set.
The proxy uses Dagger-managed provider credentials for upstream requests.

## Forwarding semantics

For the first milestone, the proxy should preserve the body and streaming shape
of the client request as much as the HTTP implementation allows.

- The request method, path, query, and body are forwarded to the configured
  upstream provider.
- Request bodies are streamed through; they are not decoded and re-encoded by
  the forwarding path.
- Response bodies are streamed back to the client as they arrive.
- Server-sent events and chunked responses remain streaming responses to the
  client.
- Unknown request fields, response fields, and streaming events are preserved.
- Provider credentials are injected upstream and are never exposed to the
  container.

Model selection needs an explicit policy because OpenAI-compatible clients send
the model in the request body. The safest first version is to pass the request
body through unchanged and configure only the upstream provider endpoint and
credentials. Model override, aliasing, or validation can be added later, but
those features require either request-body mutation or provider-specific policy.

## Tracing semantics

Tracing is best effort.

The proxy should emit a parent span for each proxied request. Child spans can be
added when the observer understands the protocol. For example, an
OpenAI-compatible observer can recognize:

- `POST /v1/chat/completions`;
- `POST /v1/responses`;
- `GET /v1/models`;
- non-streaming JSON responses;
- streaming SSE `data:` events;
- usage blocks;
- tool-call deltas and final tool calls.

If the observer cannot parse a message, it should keep the parent span and mark
the protocol details as unknown. This is still valuable because the trace shows
that an agent made a model request, how long it took, which upstream was used,
and whether the transport succeeded.

## Security model

The container receives a session-local proxy endpoint and, if needed, a
placeholder API key accepted by the proxy. It does not receive provider
credentials.

The proxy should be scoped to the Dagger session. A container or process from
another session must not be able to reuse the endpoint.

The observer must avoid recording secrets. It should redact authorization
headers, provider keys, and known secret fields. Prompt and completion content
can be revealed according to the same telemetry policy used for other Dagger LLM
spans.

## Relationship to Dagger tools

The pass-through proxy does not execute Dagger tools or mutate an `Env`.

Tool calls may still appear in observed traffic. The observer can trace them,
but it should not take over tool execution. If Dagger wants to provide tools to
the agent, that should be a separate explicit interface, such as an MCP service
or another container binding. That keeps the model API proxy lossless and avoids
silently changing the agent's protocol.

A later active mode could intentionally terminate the provider protocol and
mediate tool execution through Dagger. That mode should have a different
contract because it would no longer be pure pass-through.

## Open questions

- Should the first API be top-level, such as `llmProxy`, or attached to `LLM`
  for reuse of existing provider configuration?
- How should users choose the upstream provider when the request body is passed
  through unchanged?
- Should model validation be opt-in, and should it fail closed or only annotate
  traces?
- What should be the default content visibility policy for prompts and
  completions?
- How much buffering is acceptable for the observer before it risks affecting
  streaming latency?
- Should the proxy support Anthropic in the first milestone, or start with only
  OpenAI-compatible traffic?

## First milestone

Implement an experimental OpenAI-compatible pass-through proxy for containers:

1. Add an API that returns a session-scoped proxy `Service`.
2. Bind that service into a container and document the required
   `OPENAI_BASE_URL` and placeholder `OPENAI_API_KEY` environment variables.
3. Forward `POST /v1/chat/completions`, `POST /v1/responses`, and
   `GET /v1/models` without JSON re-encoding in the data path.
4. Preserve streaming responses.
5. Inject upstream credentials from Dagger-managed configuration.
6. Emit a parent trace span for each proxied request.
7. Add best-effort OpenAI-compatible parsing for model, usage, errors, text
   deltas, and tool-call deltas.
8. Treat parser failure as a telemetry degradation only.
9. Add an integration test with a real OpenAI-compatible client library running
   in a container, including a streaming request.

That milestone proves the useful primitive: existing agents can run unchanged
while Dagger keeps credentials out of the container and adds traces when it can
understand the traffic.
