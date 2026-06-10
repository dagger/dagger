# wcprof: engine wall-clock profiling

`wcprof` is an experimental, cheap wall-clock profiler for the Dagger engine,
built to find latency bottlenecks that CPU/memory profiles can't see (e.g. a
300ms serial tax before every container start). It is intentionally separate
from OTel: events are fixed-size structs appended to sharded in-memory
buffers with interned strings, and all heavy analysis happens offline.

## What it records

Three event types, written by hooks at the engine's blocking choke points:

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
  under the exec that made them.

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
  ..., `exec.runContainer`), plus a split of `exec.containerStart`
  (engine overhead) vs `exec.processRun` (user work).
- `core/container_exec.go`: cache-volume lock waits, mount preparation and
  output-commit phases, and the wait on the executor.
- `core/services.go`: `service_start` ops with singleflight wait edges.
- `engine/server/session.go serveQuery`: per-query `session_phase` ops
  (attachables wait, workspace load, module load, schema build, query).

## Enabling and dumping

- CLI: `dagger --profile <anything>` sets `ClientMetadata.Profile`, which
  enables the engine-global recorder for the rest of the engine's lifetime.
- Engine env: `_DAGGER_WCPROF=1` enables at startup;
  `_DAGGER_WCPROF_MAX_EVENTS=N` overrides the event cap (default ~4M).
- Dump: `GET http://<engine-debug-addr>/debug/wcprof/dump` streams a JSON
  header line (string tables, open ops) followed by one JSON event per line,
  and flushes the buffer (pass `?flush=0` to keep it).

Typical dev-engine workflow:

```bash
./hack/dev   # build + start dagger-engine.dev (publishes debug port 6060)
./hack/with-dev ./bin/dagger --profile call engine-dev container sync
curl -s http://localhost:6060/debug/wcprof/dump > /tmp/wcprof.dump
go run ./cmd/wcprof-analyze /tmp/wcprof.dump
```

## Analysis (`cmd/wcprof-analyze`, `engine/wcprof/wcanalyze`)

The analyzer reconstructs the op graph (parents, waits, nested-client
stitching) and reports:

- **what-if rankings** (the headline): a replay-based discrete-event
  simulation re-executes the recorded schedule under "class X self-time × f"
  hypotheses (f ∈ {0, 0.5, 0.9} by default) and ranks classes by how much
  end-to-end makespan each would actually save. This accounts for critical-
  path shifts, dedup (singleflighted/lazy work counted once), and dependency
  chains — unlike naive "total time per class" tables.
- per-class self-time tables (self = duration − waits − child intervals),
  with outcome counts and duplicate-execution detection.
- the end-of-workload blocking chain.
- dead air: trace gaps where no recorded op was running (= uninstrumented
  blocking, or client-side stalls).

Simulation assumptions (v1, deliberate): unlimited resources (never
CPU-bound), recorded dependency structure is invariant under the hypothesis,
waits on named resources (locks) are fixed delays. The simulated baseline
makespan is reported against the actual makespan as a drift sanity check.

## Status / caveats

This is a prototype for validating the approach:

- the recorder is engine-global (any profiled client enables it for
  everyone); per-session scoping is future work.
- events are kept until dumped; long sessions can hit the event cap (the
  dump and the analyzer both surface the drop count loudly).
- leaf I/O ops (git fetch, image pull, filesync) are not yet instrumented;
  they currently show up as self-time of their calling op, and the
  "dead air" / unexplained-self-time reports are the guide for where to add
  hooks next.
