---
name: engine-lab
description: Build and debug the Dagger engine from the workspace source with the EngineLab tools — the sandbox equivalent of the ./hack/dev + ./hack/with-dev loop. Read before using the engine-lab tools to run commands against a live from-source engine, poke its debug/pprof endpoints, run engine tests, or re-run repros after editing engine code.
---

# Engine Lab

You can build and debug the Dagger engine from the workspace source with the
EngineLab tools — the sandbox equivalent of the `./hack/dev` + `./hack/with-dev`
loop from the `engine-debugging` skill.

Workflow:

- the engine-lab start tool builds the engine + CLI from the workspace source
  and runs the engine as a persistent service with its debug endpoints enabled.
  The first build takes a while; later tools reuse the running engine.
- `dagger(args: [...])` runs `dagger <args>` against the live engine — pass the
  subcommand WITHOUT a leading "dagger" (e.g. `["call", "test"]`). The
  from-source CLI is on PATH and your CURRENT workspace tree is mounted at /src,
  re-mounted fresh on every call so your edits are always visible — only the
  engine *binary* is pinned until `restart`.
- `query` sends raw GraphQL straight to the engine (no module loaded).
- `debugGet`/`debugJq` hit the engine's :6060 debug endpoints (routes in
  cmd/engine/debug.go), e.g. /debug/pprof/goroutine?debug=2 for hangs; use
  debugJq to filter big JSON like /debug/dagql/cache.
- After editing engine code, `restart` rebuilds and replaces the running
  engine; then re-run your repro with `dagger`.
- `engineTest(pkg, run)` runs engine tests with their own ephemeral engine (no
  `start` needed), e.g. pkg "./core/integration" with run "TestSuite/TestSub".
- Engine logs: ListServices shows the engine service's span ids;
  ReadLogs(span, grep, limit) reads them — the equivalent of
  `docker logs dagger-engine.dev | grep`.
- the engine-lab start tool prints the engine's tcp://<host>:1234 endpoint
  (`endpoint` re-prints it). If tui-qa tools are available, pass it to their
  start tool's `engine` arg to run a TUI session against THIS engine — then
  `debugGet`/`debugJq`/ReadLogs introspect the very engine the TUI is driving
  (e.g. reproduce a hang in the TUI, then pprof it live). `restart` and the
  engine-lab stop tool break attached TUI sessions; restart them after.
- the engine-lab stop tool when done.
