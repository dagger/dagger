---
name: memory-cake
description: Measure the Dagger engine's (or CLI's) heap under scripted workloads with the memory-cake module — per-PR memory reproducers (demo<PR>), measure/measureWorkload, and the cake comparing two refs. Read before reproducing or verifying a memory leak or retention fix, adding a memory demo for a PR, or comparing heap between branches.
---

# Memory cake

`.dagger/modules/memory-cake` measures heap retained by an engine built from
any ref of github.com/dagger/dagger while a scripted workload runs. It is an
on-demand tool, not a CI check. Its functions are not tools: call them from
Dang (DangEval, workspace scope) as `memoryCake.<fn>`, or with the CLI
(`dagger call memory-cake ...`).

## Running

```dang
let m = memoryCake.demo15213(ref: "main", env: ["FILES=2000", "EDITS=20"])
[m.summary, m.focusTop]
```

- `demo<PR>(ref, env)`: one PR's reproducer on one ref. `env` scales the
  workload (`KEY=VALUE`, read by its workload.sh); each demo's doc names its
  knobs and defaults. Defaults are sized to show the leak clearly (minutes,
  hundreds of MiB); shrink them for a smoke test.
- `measure(pr, ref, env, settle)`: the same, by PR number.
- `measureWorkload(pr, ref, workload, focus, focusAlloc, cli, env, settle)`:
  any directory holding a workload.sh, for trying a workload before it is a
  demo.
- A `Measurement` has `baselineBytes` (after a trivial warm-up session),
  `liveBytes` (end of the workload, its session still open), `afterBytes`
  (session closed and settled), the focus slices of each (`focusBytes`,
  `focusBaselineBytes`, `focusAfterBytes`), `focusTop` (heaviest focus
  sites), alloc_space totals (`allocBytes`, `focusAllocBytes`,
  `focusAllocTop`), the profiles (`liveProfile`, ...), `liveVars`,
  `liveClients` and `out` (everything the driver wrote), and `summary`.
- `cake(before: "main", after: <ref>, prs: [...])`: every demo (or those in
  `prs`) on both refs, as a markdown table. `cakeJson` returns the same run
  as JSON; `render/` (`go run . -out /tmp/cake doc.json ...`) draws it.
  For a clean comparison, make `before` the commit `after` is built on.
- `tracePeaks(ref, env)`: transient allocation of the LLM trace tools
  (FindSpans/FindCalls), which is not retained heap and so not in the cake.

Measurements and `engine` are cached per session: read every field you need
from one handle in ONE DangEval (or one `dagger -c` script). Reading fields
in separate calls re-runs the whole measurement.

Run ONE engine at a time: never two measurements concurrently (two
DangEvals, or a `cake` beside a demo). Each measurement builds the ref's
engine and CLI, runs a fresh engine with its own state volume, and stops it
when the measurement ends.

## Adding a demo

1. Write `fixtures/<PR>/workload.sh` (anything else it needs goes beside it).
2. Add a `Demo(pr, title, focus, ...)` entry to `demos` in main.dang.
3. Add a `demo<PR>(ref, env)` function calling `measure`, documented with
   what it does, its env knobs and defaults, and the numbers it shows.

### workload.sh conventions

- POSIX `sh` (alpine), `set -eu`. Its cwd is the fixture directory; on PATH:
  `dagger` (the ref's CLI), `curl`, `jq`, `heapstat`, `spangen`.
- It runs INSIDE the open session (driver.sh runs it under `dagger run`):
  every `dagger` command it runs joins that session as a nested client, so
  session-scoped memory stays live until the live heap is taken. To open a
  separate top-level session instead, unset `DAGGER_SESSION_PORT` and
  `DAGGER_SESSION_TOKEN` for that command (see fixtures/15212).
- Read sizes from env with defaults (`FILES=${FILES:-20000}`) so callers can
  scale it. Keep the engine under ~4 GB at the defaults.
- Prefer `dagger api query --no-load-module` with the query on stdin; pass
  variables with `--var-json '{"x": ...}'` (build it with `jq -n`), not
  `--var`.
- Load fixture modules with absolute paths: `-m "$(pwd)/grower"`.
- The engine's debug server is `http://dagger-engine:6060` (`/debug/vars`,
  `/debug/client-lifecycle`, `/debug/dagql/cache`) if the workload wants to
  record extra state into `/out`.
- Make values distinct when you need work to happen (a counter, or
  `$CAKE_NONCE`): a cached call does no work and emits no spans.

### Choosing the focus regexp

`focus` is a regexp over stack frames (`<function> <file>`); samples with a
matching frame are the demo's focus bytes. Don't guess it: run the workload
first with `measureWorkload` and an empty or broad focus, then read
`focusTop`, or the live profile itself (`liveProfile`,
`go tool pprof -top -sample_index=inuse_space`), and pick the frames that
allocate or keep what the PR frees. Prefer frames the PR touches on both
sides (so before and after measure the same thing), and leave out
structures that are the same on both sides (e.g. an engine-wide LRU). heapstat
also accepts ` && ` and ` || ` clauses. `focusAlloc` (alloc_space) is for
churn a PR stops rather than memory it frees.

### Live vs after

`liveBytes` is taken with the session open, `afterBytes` once it closed and
settled. A leak that is session-scoped (memos, caches owned by the session
or a client) shows live and goes away after; one that outlives the session
(engine-wide metadata) shows in both. Say which in the demo's doc.

### CLI-heap mode

`cli: true` measures the `dagger run` CLI's heap instead of the engine's:
its pretty TUI runs headlessly (DAGGER_TUI_CONSOLE at `$CAKE_CONSOLE`, pprof
at `$CAKE_HEAP_URL`), so its frontend ingests and renders telemetry as an
interactive one does. The CLI demos share `fixtures/cli`, where `spangen`
(spangen/) streams a long agent session's spans, logs and metrics from an
exec; scale with `SPANS`, `EXPORTS`, etc. A CLI workload must wait until the
CLI has ingested its telemetry before returning (fixtures/cli polls the
console's `/spans`).

### Lessons

- The engine's telemetry indexes are built only when something reads them:
  start a span/log workload with a log search (fixtures/15220), or nothing
  is indexed.
- Sharing across clients only shows with CONCURRENT clients: sequential
  clients each come and go, so per-client memory never accumulates
  (fixtures/15216 holds its clients open with a sleeping exec).
- Nested clients can't prune: only a session's main client may, so prune
  from a separate session (fixtures/15212).
- Cached calls emit no spans and do no work: make each call distinct.
- One engine at a time; small sizes when iterating.
- In Dang, read several fields of one object in one selection,
  `ctr.{{exitCode, stderr}}`, rather than one call per field.

## Fixtures that are not demos

`fixtures/15226-go` and `fixtures/15226-go-dang` reproduce #15226's
receiver retention with a Go module (legacy runtime) and a Go module behind
a Dang entrypoint. They are not registered demos: they reproduce what
#15226 does not fix yet (open issues). Run them with `measureWorkload`, as
their headers show.
