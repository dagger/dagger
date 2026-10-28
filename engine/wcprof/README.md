# wcprof: engine wall-clock profiling

`wcprof` is an experimental, cheap wall-clock profiler for the Dagger engine,
built to find latency bottlenecks that CPU/memory profiles can't see (e.g. a
300ms serial tax before every container start). It is intentionally separate
from OTel: events are fixed-size structs appended to sharded in-memory
buffers with interned strings, and all heavy analysis happens offline.

## What it records

Four event types, written by hooks at the engine's blocking choke points:

- **ops**: timed intervals with a *kind* (`call`, `call_exec`, `lazy`, `exec`,
  `exec_phase`, `service_start`, `session_phase`, ...), a *class* (e.g.
  `Container.withExec`, `exec.setupNetwork`), an instance ident (recipe
  digest, exec ID), a structural parent (from context), and an outcome
  (`hit`/`executed`/`joined`/...).
- **waits**: exact blocked-on intervals — who waited, on which op (or named
  resource, e.g. a cache-volume lock), why, from when to when. These are
  recorded *at the choke points themselves* (dagql singleflight, lazy-eval
  waiters, service starts, exec completion), not inferred from span nesting.
- **links**: non-blocking correlations, most importantly "exec op X hosts
  nested client Y" so module-function calls back into the API are stitched
  under the exec that made them (wcprof-report puts them under the exec's
  `exec.processRun`, or its `exec.workload` when the run is split).
- **spans**: the OTel span an op owns, when telemetry is on and the engine
  emits a span for the same work: a `call` with its own span, `call_exec`,
  `dagql.publishResult`, `lazy`, `exec`, its `exec.containerStart`,
  `exec.runtimeStart` and `exec.processRun` phases, and `service_start`.
  Dumped as `{"e":"span","id":<op>,"tr":<trace ID>,"sp":<span ID>}` in hex,
  so offline analysis can join an op to its span's attributes exactly.

Current hook points:

- `dagql/cache.go getOrInitCall`: one `call` op per caller (cache hits and
  do-not-cache calls included — this deliberately records what OTel
  suppresses), one shared `call_exec` op per actual resolver execution,
  wait edges from every caller to it, and a `dagql.publishResult` op for
  publication cost.
- `dagql/cache.go evaluateOne`: one `lazy` op per lazy-evaluation run, classed
  by the call that created the lazy value, with wait edges from the
  triggering and joining ops.
- `engine/engineutil/executor.go`: one `exec` op per container run, one
  `exec_phase` op per setup phase (`exec.setupNetwork`, `exec.setupRootfs`,
  ..., `exec.runContainer`), plus a split of the container run into
  `exec.containerStart` (engine overhead up to launching runc),
  `exec.runtimeStart` (runc creating the container, up to the pid file it
  writes once the workload is released) and `exec.processRun` (user work).
  When the container runs under the injected `/.init`, `/.init` reports its
  own timing on a pipe and `exec.processRun` is split further into
  `exec.initStart` (exec of `/.init` up to its main), `exec.processSpawn`
  (its setup and the fork/exec of the command), `exec.workload` (the command,
  user work) and `exec.processExit` (`/.init` exiting, runc reaping it and
  exiting, and the stdio drain).
  The exec op is linked to every nested client the container connects.
- `core/lazy_state.go`, `dagql/cache.go syncResultSnapshotLeases`: lock waits
  on a lazy group's body (`core.lazyGroupOnce.mu`), the whole-op latch
  (`core.LazyState.LazyMu`) and a result's snapshot lease sync
  (`dagql.sharedResult.leaseSyncMu`), recorded only when the lock is
  contended (`wcprof.Lock`).
- `core/container_exec.go`: cache-volume lock waits, mount preparation and
  output-commit phases, and the wait on the executor.
- `core/services.go`: `service_start` ops with singleflight wait edges.
- `engine/server/session.go serveQuery`: per-query `session_phase` ops
  (attachables wait, workspace load, module load, schema build, query).

## Enabling and dumping

Two scopes:

- **Per-session**: `dagger --profile <anything>` (hidden flag) sets
  `ClientMetadata.Profile`. Only that session's work is recorded — including
  its nested module/SDK clients — via a context mark applied by the session
  server and propagated to derived contexts. Caveat: if a profiled call joins
  in-flight identical work spawned by an *unprofiled* session, the join shows
  up as an unresolved wait (modeled by the analyzer as a fixed delay) since
  the other session's execution wasn't recorded.
- **Engine-global**: record everything. Enable at startup with the engine's
  `--wcprof` flag (hidden), or at runtime by POSTing to
  the debug endpoint:

  ```bash
  curl -X POST -d on  http://<engine-debug-addr>/debug/wcprof/enabled
  curl -X POST -d off http://<engine-debug-addr>/debug/wcprof/enabled
  curl http://<engine-debug-addr>/debug/wcprof/enabled   # report state
  ```

  POSTing `off` stops engine-global recording but keeps buffered events
  dumpable, lets `--profile` sessions keep recording, and lets in-flight
  recorded ops finish; `on` re-enables on the same recorder (dumps from
  before and after merge cleanly since the epoch is unchanged).

The recorder keeps up to ~4M events (about 300 MB); events past that are
dropped and counted in the dump header's `dropped_events`.

- Dump: `GET http://<engine-debug-addr>/debug/wcprof/dump` streams a JSON
  header line (string tables, open ops) followed by one JSON event per line,
  and flushes the buffer (pass `?flush=0` to keep it).
- Dump at stop: with engine-global recording on, a stopping engine writes the
  whole buffer, unflushed, to `<engine root>/wcprof/stop.ndjson.gz`
  (`wcprof.StopDumpPath`): on the stop signal (`engine.stop.signal`), when
  each stop phase starts and ends (`engine.stop.<phase>` ops under
  `engine.stop`, waiting for a running garbage collection included), when the
  server's stop ends, and when each telemetry drain after it starts and ends
  (`engine.exit.<step>`). Each write replaces the file atomically and is
  itself a `wcprof.stopDump` op, so an engine killed mid-stop leaves the last
  complete dump, with the phase it was in as an open op. Writes run in the
  background, one at a time, so the stop doesn't wait for them (a large
  recording's dump takes seconds to write): requests made during a write
  become one more write, from the state at its start. The dump on disk lags
  the stop by at most the write in progress and one more; as it exits, the
  engine waits for one last write, so a stop that finishes leaves its whole
  record. The write that produced a dump is open in it too, as a
  `wcprof.stopDump` op, since it snapshots the recorder before it ends. So
  after a fully drained stop, the dump an engine leaves has that write as its
  only open op; a phase still running when the stop gave up on it (its
  deadline passed) stays open beside it. In a dump taken mid-stop, the work
  running at the snapshot is open as well: the stop and its current phase. An
  open op alone doesn't prove the work was cut off.

Typical dev-engine workflow:

```bash
./hack/dev   # build + start dagger-engine.dev (publishes debug port 6060)
./hack/with-dev ./bin/dagger --profile call engine-dev container sync
curl -s http://localhost:6060/debug/wcprof/dump > /tmp/wcprof.dump
```

Dumps are NDJSON: a `DumpHeader` line (with the interned `strings` table that
`class`/`ident`/`client` indexes point into), then one `DumpEvent` per line.
Read them with `wcprof.ReadDump`.

## Analysis

This package is only the recorder; analysis tooling lives outside this repo.
The offline what-if analyzer (`cmd/wcprof-analyze`, `engine/wcprof/wcanalyze`:
replay-based what-if rankings, blocking chains, dead air) was moved out
in #13588.

For agent-driven debugging, the engine-lab dev module
(`.dagger/modules/engine-lab`) has `wcprofEnable`, `wcprofCapture` and
`wcprofReport` tools: capture a dump from a from-source engine, then report
per-class self time, per-parent child breakdowns, per-client activity, op
subtrees, or name-resolved events through jq. Its `wcprof-report` helper is
built against this package at tool-call time, so it tracks the dump format.
See the engine-lab skill for the workflow.

## Status / caveats

This is a prototype for validating the approach:

- there is one engine-wide recorder and event buffer; per-session enablement
  scopes what gets *recorded*, but all enabled sessions share the buffer and
  dumps (the analyzer can group by client/root, but the dump endpoint has no
  per-session filter).
- events are kept until dumped; long sessions can hit the event cap (the
  dump and the analyzer both surface the drop count loudly).
- leaf I/O ops (git fetch, image pull, filesync) are not yet instrumented;
  they currently show up as self-time of their calling op, and the
  "dead air" / unexplained-self-time reports are the guide for where to add
  hooks next.
