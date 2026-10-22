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
  under the exec that made them (wcprof-report puts them under the exec's
  `exec.processRun`, or its `exec.workload` when the run is split). A `fact`
  link says something was true of its op (its ident, e.g. `hit.remote`),
  optionally about a result; a `part` link says a result has a part (its
  ident) of `b` bytes.

Current hook points:

- `dagql/cache.go getOrInitCall`: one `call` op per caller (cache hits and
  do-not-cache calls included — this deliberately records what OTel
  suppresses), one shared `call_exec` op per actual resolver execution,
  wait edges from every caller to it, and a `dagql.publishResult` op for
  publication cost. A publication that waits for the dependency attachment
  another publication, or a merge, holds open on its recipe's entry records an
  `attach` wait on that publication's op (in a trace, a wait link to its
  span; a merge has no span to link to).
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
- `dagql/cache.go getOrInitCall`, lookup facts: `fact` links from the `call`
  op saying why its lookup decided what it did, named as its span's
  `dagger.io/cache.*` attributes without the prefix: `hit.remote` (about
  the result hit), `miss.saw_expired`, `miss.expired_remote`,
  `miss.incompatible_candidates`, `miss.incompatible_remote`,
  `miss.chosen_remote`, `miss.producer_released`, `miss.requirements_grew`,
  `miss.unknown_input`, `miss.fresh_inputs`, `adopted` and
  `adopted.remote`. They read the call's `CacheDecision`; when no recording
  span armed one, the profile arms it for the call and disarms it after (the
  carrier's own fact gathering, such as the new-inputs check, is then the
  profile's only added cost).
  Lookups and publications record contended waits for the cache's graph
  lock as lock waits on `dagql.Cache.egraphMu`, as an export's walk does.
- `engine/server/resolver`, registry I/O: an `io` op per image pull
  (`image.pull`), resolve (`image.resolve`) and push (`image.push`), with the
  ref as ident and external work type. Under a pull, an `image.fetch` op
  per blob downloaded (its digest and the bytes read); under
  a push, an `image.push.blob` op per blob (its digest, and its size when it
  was sent rather than already in the registry).
- `engine/snapshots` layer imports (remote-cache installs and image pulls):
  per layer, an `io` op `snapshot.import.fetch[<source>]` (`download`,
  `builtin` or `present`) and `snapshot.import.apply`, with the layer's
  digest as ident and its size in bytes, and a lock wait on
  `snapshots.importLayer`.
- Remote-cache installs (`dagql/cache_part_content.go`): one `remote_cache`
  op `remotecache.install` per attempt to install an offered part, with the
  part key as ident, the receiving result, and the bytes downloaded; a `part`
  link with the part's size (its whole chain's); the layers' import ops,
  `remotecache.renew` (fresh content addresses) and
  `remotecache.install.commit` as children. Outcomes are `ok`, `refused`,
  `superseded`, `canceled` or `error` (with an `install.content` fact when
  the source's bytes were bad). A part that no source could supply and that
  is computed locally gets a `part.no_source` fact. Lazy ops name their task
  (`lazy:…`, `acquire:…`, `obtain:…`) as ident. These read the same
  measurement points as the install's log line and span attributes.
- Remote-cache exports (`engine/remotecache/session.go`,
  `engine/server/remote_cache_protocol.go`, `dagql/cache_value_capture.go`):
  one root `remote_cache` op `remotecache.export` per export the service
  asks for, named by its export ID, with the bytes uploaded and an
  `export.no_bundle` or `export.empty` fact when it sent nothing or the
  service asked for nothing. It waits on `remotecache.exportSlots` and on
  `remotecache:service-upload` (the service's upload request). Its children
  are `remotecache.export.hold`, `remotecache.export.capture` (per attempt;
  a graph-lock wait on `dagql.Cache.egraphMu`, `.walk`, `.copy` and
  `.chains`, with one `remotecache.export.chain` per exported part: the
  part key, its result and the bytes of the blobs it computed, over its
  layers' `snapshot.export.diff` ops and `snapshots.exportLayer` lock
  waits), `remotecache.export.probe` per root probed after a failed
  capture, and `remotecache.upload`, with a `remotecache.upload.blob` per
  PUT after a wait on `remotecache.uploadSlots`. The capture links each
  output's part to its result with the part's size (its chain's). Merges are
  a `remotecache.merge` op, and each offered part a `remotecache.offer` op
  on the row its address resolved to, with an `offer.<outcome>` fact.
  Exports, merges and offers run on the service's connection, not a
  client's, so they are recorded when the engine records everything, not
  under `--profile`. Image exports get the `snapshot.export.diff` ops too.

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
  `--wcprof` flag (hidden) or `_DAGGER_WCPROF=1`, or at runtime by POSTing to
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

`_DAGGER_WCPROF_MAX_EVENTS=N` overrides the event cap (default ~4M),
read when the recorder is first created.

- Dump: `GET http://<engine-debug-addr>/debug/wcprof/dump` streams a JSON
  header line (string tables, open ops) followed by one JSON event per line,
  and flushes the buffer (pass `?flush=0` to keep it).

Typical dev-engine workflow:

```bash
./hack/dev   # build + start dagger-engine.dev (publishes debug port 6060)
./hack/with-dev ./bin/dagger --profile call engine-dev container sync
curl -s http://localhost:6060/debug/wcprof/dump > /tmp/wcprof.dump
```

Dumps are NDJSON: a `DumpHeader` line (with the interned `strings` table that
`class`/`ident`/`client` indexes point into), then one `DumpEvent` per line.
An event's `b` is a byte count: what an op moved, or a `part` link's size.
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
- some leaf I/O (filesync, HTTP fetches, module loading) is not yet
  instrumented; it shows up as self-time of its calling op, and the
  "dead air" / unexplained-self-time reports are the guide for where to add
  hooks next.
