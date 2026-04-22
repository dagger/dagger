# Python SDK Loading Performance — Investigation & Proposed Changes

Date: 2026-04-21
Status: Draft for review

## 1. Context

The inner dev loop for a Python Dagger module usually looks like:

```
dagger init --sdk=python
dagger functions
dagger call <fn> <args>
<edit module source>
dagger call <fn> <args>
```

This document investigates where time goes in that loop, focused on the Go
layers that sit between the CLI and the user's Python container:

- `core/sdk/*.go` — how the builtin Python SDK is imported, wrapped in a
  dagql.Server, and installed as a module.
- `sdk/python/runtime/*.go` — the Python SDK's own Go runtime module
  (Discovery, container composition: WithBase, WithSDK, WithTemplate,
  WithSource, WithUpdates, WithInstall).
- The seam between them (`loadBuiltinSDK` → `newModuleSDK` → `moduleRuntime`).

Out of scope: uv/pip internals, buildkit/image-pull internals, non-Python
SDKs.

## 2. Scenarios & methodology

One measurement run in a dev-engine playground, `--progress=plain -vvv`,
executing the following back-to-back in a single engine session:

| ID  | Action                                                |
|-----|-------------------------------------------------------|
| S1a | `dagger init --sdk=python --name=testperf`            |
| S1b | `dagger functions` (cold from init)                   |
| S1c | `dagger call container-echo --string-arg hello stdout` (first call) |
| S2a | `dagger call …` (warm repeat 1)                       |
| S2b | `dagger call …` (warm repeat 2)                       |
| —   | append `# cache-bust-<ts>` line to `main.py`          |
| S3a | `dagger functions` (after edit)                       |
| S3b | `dagger call …` (after edit)                          |

Per-scenario stderr was captured to individual files. Span durations were
read from the progress renderer output (e.g. `load SDK: python DONE [11.5s]`).
Wall-clock per command was not captured reliably (the playground `sh` lacks
`date +%N`); per-span timings are the authoritative data.

Raw logs were not committed. Figures below are quoted directly from the
captured span output.

## 3. Findings summary

### 3.1 Per-scenario log volume

| Scenario          | Log bytes |
|-------------------|-----------|
| S1a_init          | 62,076    |
| S1b_func_cold     | 52,847    |
| S1c_call_cold     | 25,844    |
| S2a_call_warm1    | 16,712    |
| S2b_call_warm2    | 16,712    |
| S3a_func_edited   | 43,642    |
| S3b_call_edited   | 20,480    |

`S2a == S2b` exactly. The system is deterministic and caches well on the
warm path.

### 3.2 Notable span durations (from progress output)

- **S1a**: `load SDK: python` DONE **[11.5s]** — *first-ever* load of the
  builtin Python SDK in this engine session.
- **S1a**: `module SDK: run codegen` DONE **[4.1s]** — first codegen for
  the freshly-initialized user module.
- **S1a**: `Container.asService` (the Python base image pull) and `uv lock`
  visible in the trace.
- **S1b → S3b**: `load SDK: python` CACHED [0.0s] on every subsequent
  command.
- **S1b → S3b**: `pythonSdk` (SDK constructor result) CACHED on every
  subsequent command.
- **S2b** (warm repeat): the top-level span chain is
  `load workspace: . [1.1s]` → `loading type definitions [1.1s]` →
  `load module: testperf [0.9s]` → `ModuleSource.asModule [0.9s]` →
  `asModule getModDef [0.9s]`. Every inner sub-op is CACHED [0.0s].
  **0.9s of warm overhead is spent in `asModule getModDef` despite
  everything inside it being cached.** This is the user module's schema
  reconstruction, *not* the SDK.

### 3.3 SDK-specific span occurrences

Counts of key span/field substrings per scenario log:

| Scenario          | load SDK | load runtime | run codegen | asModuleSource | subpath | modName | configExists |
|-------------------|:--------:|:------------:|:-----------:|:--------------:|:-------:|:-------:|:------------:|
| S1a_init          | 4        | 0            | 2           | 2              | 4       | 2       | 4            |
| S1b_func_cold     | 2        | 2            | 0           | 2              | 2       | 2       | 2            |
| S1c_call_cold     | 2        | 2            | 0           | 2              | 0       | 0       | 0            |
| S2a_call_warm1    | 2        | 2            | 0           | 2              | 0       | 0       | 0            |
| S2b_call_warm2    | 2        | 2            | 0           | 2              | 0       | 0       | 0            |
| S3a_func_edited   | 2        | 2            | 0           | 2              | 2       | 2       | 2            |
| S3b_call_edited   | 2        | 2            | 0           | 2              | 0       | 0       | 0            |

`subpath`/`modName`/`configExists` are the graphql fields the Python SDK's
Discovery queries. They drop to zero on the warm path and re-fire on edits.

## 4. Hypothesis verdicts

The investigation started from three hypotheses. The data largely refutes
all three for the typical warm/edit dev loop.

### H-a — "Each call rebuilds the SDK dagql server from scratch"

**Refuted** for the warm path. The strong form — that every `dagger call`
pays a full rebuild — is not what the traces show. After S1a, every
subsequent `load SDK: python` span is CACHED [0.0s]. The
`pythonSdk` constructor result is CACHED, as is the underlying container
composition (~15 CACHED container ops per load).

Weak form of the hypothesis is accurate but not impactful: a `dagql.Server`
is instantiated for the SDK on each invocation, but dagql's
content-addressed cache makes this essentially free in warm state.

### H-b — "Python SDK discovery makes too many round-trips"

**Refuted** for the warm path, **partially supported** on edits.

- S2a / S2b / S1c: discovery's graphql fields (`sourceSubpath`,
  `moduleOriginalName`, `configExists`) do not appear at all — the
  concurrent fan-out is fully absorbed by the cache.
- S3a (edit + functions): discovery does re-fire (subpath=2, modName=2,
  configExists=2) but completes inside a broader `module SDK: load runtime`
  span that is not a visible bottleneck.

Even on edits, the cost of discovery is dominated by downstream
container-composition work, not by the round-trips themselves.

### H-c — "Redundancy between `dagger functions` and `dagger call`"

**Refuted in sequence.** `dagger call` after `dagger functions` consistently
shows `module SDK: load runtime` DONE [0.0s] and all container ops CACHED.
The Python SDK's `Common` (called by both `ModuleRuntime` and the typedefs
path) + dagql's content-addressed cache already unify the work.

A residual question: if a user runs `dagger call` as their *first* command
(no preceding `dagger functions`), does the runtime start cold? Not measured
in this run; based on the code path it would be equivalent work since both
commands trigger module loading. Candidate follow-up.

## 5. Emergent findings

These were not on the hypothesis list but surfaced during measurement.

### 5.1 First-time `load SDK: python` is 11.5s

The first time any command asks the engine to load the builtin Python SDK,
`load SDK: python` takes ~11.5s. This is:

1. `_builtinContainer(digest).rootfs` — materialize the builtin SDK's
   rootfs.
2. `directory("runtime").asModuleSource().asModule(forceDefaultFunctionCaching=true)`
   — treat the Python SDK's Go runtime subdir as a module. For a Go
   module, `asModule` goes through the Go SDK path, which compiles and
   runs a Go binary to register type definitions.
3. `newModuleSDK`: create a dagql.Server, install the module + default
   deps, call the constructor, resolve implemented SDK functions.

After this first invocation, the entire chain is cached for the rest of the
engine session.

### 5.2 Warm per-call overhead sits in `asModule getModDef` (0.9s)

On a fully-cached warm call (S2b), the observable span breakdown is:

```
load workspace: .                               [1.1s]
└── loading type definitions                    [1.1s]
    └── load module: testperf                   [0.9s]
        └── ModuleSource.asModule               [0.9s]
            └── asModule getModDef              [0.9s]
                ├── Missing._implementationScoped  CACHED [0.0s]
                ├── load sdk runtime              CACHED [0.0s]
                └── module                        CACHED [0.0s]
```

Every sub-operation inside `asModule getModDef` is CACHED, yet the span
itself consumes ~0.9s. That time lives in Go engine code
(`core/schema/modulesource.go:2758`), not in a container exec or graphql
round-trip. It is paid *every* CLI invocation regardless of whether source
changed. It is unrelated to SDK loading per se — this is the user
module's definition reconstruction — but it dominates the warm dev-loop
overhead users actually feel.

## 6. Proposed changes

Ranked by expected impact. Each is described at framing level; each would
become its own implementation plan.

### P1 — Precompile/pre-warm the Python SDK's Go runtime into the engine image

**Problem:** First-ever `load SDK: python` is ~11.5s. New users and CI
workers pay this on their first Python module command.

**Idea:** At engine build time, pre-run the Go `asModule` work for the
builtin Python SDK so the dagql cache entry is already populated when the
engine boots. Options:

- Ship a precompiled Go runtime binary + cached typedef metadata alongside
  the builtin SDK container (leveraging `forceDefaultFunctionCaching=true`
  to seed the cache on first boot).
- Eagerly call `loadBuiltinSDK(sdkPython)` during engine startup in a
  background goroutine so the first real call hits a warm cache.

**Expected impact:** Remove ~11.5s from the first-Python-module-ever
experience. Zero effect on subsequent commands.

**Risks:** Engine-image size/complexity; correctness of the precompiled
cache entry across CPU architectures / engine versions; startup latency
tradeoff if we eager-load.

**Location:** `core/sdk/loader.go`, `engine/distconsts/*`, build scripts.

### P2 — Reduce cost of `asModule getModDef` on warm path

**Problem:** 0.9s per CLI call is paid even when everything is cached. For
a "nothing changed" re-run, this is pure overhead.

**Idea:** Investigate what the 0.9s represents in the Go engine. Candidate
causes:

- dagql cache-lookup traversal cost for the full module definition graph.
- Schema/typedef normalization re-run every invocation.
- Serialization overhead in returning the Module object across the
  getModDef "special function" boundary.

**Expected impact:** If we can cut the warm per-call overhead in half,
every Dagger CLI invocation against an unchanged module becomes
noticeably snappier. This affects all SDKs, not just Python.

**Risks:** Schema correctness and cache key invariants. Changes here
touch the core module-loading pipeline (`core/schema/modulesource.go`);
blast radius is wider than Python.

**Location:** `core/schema/modulesource.go:2758` and the dagql cache
machinery around it.

### P3 — Characterize and shrink the edit-then-functions-then-call cost

**Problem:** S3a (edit + functions) has 2.6× the log volume of warm S2
(43,642 vs 16,712 bytes). Some of this is inescapable (source changed, must
re-scope); some may be avoidable.

**Idea:** Profile S3a to identify what re-fires that didn't need to. Look
at whether Discovery's re-runs can be short-circuited when only non-config
source changed (e.g. edits to `main.py` don't affect `pyproject.toml` or
base-image resolution, so the WithBase/uv paths shouldn't need
reconsideration).

**Expected impact:** Unknown until measured; likely small absolute gains
but important for the realistic dev-loop experience (most iterations
involve an edit).

**Risks:** Cache-key design requires care; false cache reuse across
edits would be a correctness bug.

**Location:** `sdk/python/runtime/discovery.go`, `sdk/python/runtime/main.go`,
and the dagql digest keys used for downstream container ops.

### Priority order

1. **P1** (precompile Python SDK runtime) — biggest user-visible gain, most
   localized change. Do first.
2. **P2** (warm-call getModDef overhead) — broader impact across SDKs, but
   larger blast radius and needs investigation before implementation.
3. **P3** (edit-path characterization) — lower-priority polish; may fold
   into P2 if findings overlap.

## 7. Non-goals

- Changes to uv / pip / image-layering *inside* the built Python container.
- Cross-SDK refactoring, even if P1 and P2 findings generalize (e.g. to
  TypeScript); this design stays Python-focused.
- Architectural changes to dagql caching or the broader module-loading
  pipeline beyond what P2 requires.
- Client-side CLI startup optimization (engine-connection, session setup).

## 8. Open questions

- Is the dagql cache persisted across engine restarts for the builtin SDK
  `asModule` result, or re-computed each engine boot? If persistent, P1's
  value is bounded to first-engine-boot; if not, P1 pays off on every
  engine restart.
- What is the Go-side breakdown of `asModule getModDef`'s 0.9s? A short
  profiling pass (pprof or targeted tracing) would tell us whether P2 is
  worth pursuing before committing to a plan.
- What does "cold first-ever `dagger call`" (no preceding `dagger
  functions`) look like? Was not measured in this run.
- Does P1's precompilation need to be per-architecture, and how does that
  interact with the engine container's multi-arch build?

## 9. Changelog

- 2026-04-21: Initial draft written after measurement run. Three original
  hypotheses (H-a, H-b, H-c) refuted; proposed changes pivoted to P1
  (precompile Python SDK runtime), P2 (warm getModDef overhead), P3
  (edit-path characterization).
