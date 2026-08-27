---
name: mcp-lab
description: QA the LLM-facing MCP surface (dagger mcp) against a from-source engine — see verbatim tool results, especially for service-crash scenarios.
---

# mcp-lab: be the model

Use mcp-lab when you need to see EXACTLY what an LLM sees through `dagger
mcp` — raw tool results, isError markers, builtin ReadLogs/ListServices
output — rather than a TUI rendering or your own session's differently
composed view.

## The loop

1. (Engine-change QA) `engine-lab start` — note the printed tcp endpoint.
2. `mcp-lab start` with `engine: <that endpoint>`. Omit `engine` to attach to
   the current session's own engine instead (stock behavior, no rebuild).
3. `tools` — the verbatim tools/list a model would receive.
4. `call(name, argsJson)` — the verbatim tools/call result. isError results
   are prefixed with a marker, not raised: failure UX is the point.
5. After engine edits: `engine-lab restart`, then `mcp-lab start` again (the
   old session died with the old engine).

The bridge's own service logs (ListServices in YOUR session, then ReadLogs)
carry the spawned CLI's stderr — progress and engine chatter — useful when a
call hangs or the session fails to initialize.

## Crash scenarios (fixtures/crasher)

The default fixture serves these MCP tools alongside the builtins:

- `startCrasher` (`{"delaySeconds": N}`, default 30): healthy service that
  prints a FATAL line and exits 7 after N seconds — crash mid-session.
- `startDoomed`: dies before its healthcheck, so the start call itself fails —
  crash on boot. The interesting part is the error text the model receives.
- `poke` (`{"url": "http://<hostname>:8080"}`): probe whether a service still
  answers, and observe the failure UX when it doesn't.

Questions worth asking in a crash QA pass: does the failure hand the model a
usable span handle? Does ListServices admit the service ever existed? Does
ReadLogs on the advertised handle actually reach the crash logs?

## Pitfalls

- The first `tools`/`call` after `start` can take minutes (SDK builds in the
  attached engine). The bridge holds requests until init completes and
  reports init failures in the response body instead of dying.
- One MCP session = one `dagger mcp` process = one dagger session: services
  started by earlier `call`s stay alive for later ones. `start` again for a
  fresh session.
- `call` argsJson must be a JSON OBJECT (`{}` for no args).
