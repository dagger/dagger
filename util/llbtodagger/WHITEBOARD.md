# LLB -> Dagger ID Whiteboard

Last updated: 2026-02-28

## Explicit Goal and Hard Requirements (Do Not Forget)
- Goal: implement a utility library under `./util/llbtodagger` that converts BuildKit LLB (`*pb.Definition`) into a Dagger `*call.ID`.
- Required input/output:
  - Input: `*pb.Definition` (BuildKit LLB definition).
  - Output: `*call.ID` (must reference Dagger API calls/fields).
- Primary parsing utility to use: `engine/buildkit/llbdefinition.go` (`DefToDAG` / `OpDAG`).
- We should support "almost all" LLB operations where there is a meaningful Dagger API representation.
- If an LLB construct cannot be represented faithfully in Dagger API ID form, return an error immediately.
- This file (`WHITEBOARD.md`) is the persistent task log for:
  - requirements,
  - plan/checklist,
  - gotchas,
  - progress updates,
  - unresolved mismatch cases.

## Current Working Assumptions
- Primary target is LLB produced in Dagger engine workflows; support for arbitrary external LLB is best-effort.
- We should prioritize deterministic and explainable mappings over trying to "guess" hidden runtime state.

## Conversion Principles (Do Not Violate)
- Fail fast on unsupported or imperfect mappings.
  - If an op or field cannot be represented faithfully yet, return an error.
  - Do not add fallback behavior yet.
  - Do not silently skip unsupported data.
- Do not implement any custom-op parsing/decoding path.
  - `dagger.customOp` handling is intentionally out of scope.
  - Custom ops are being removed in parallel work; this library should not depend on them.
- Focus scope: convert LLB ops to Dagger IDs.
  - Do not parse Dockerfiles in this package.
  - This package exists to map already-produced LLB into Dagger IDs.

## Progress Snapshot
- [x] Read `cache-expert` core references and debugging guidance.
- [x] Reviewed `engine/buildkit/llbdefinition.go` (`OpDAG`, op type helpers).
- [x] Reviewed `dagql/call/id.go` construction/digest rules.
- [x] Reviewed schema entry points for `container`, `directory`, `file`, `git`, `http`, `host`.
- [x] Initialized this whiteboard with explicit goal/requirements/input-output.
- [x] Implement package scaffolding in `util/llbtodagger`.
- [x] Implement first conversion library pass + initial tests.
- [x] Expanded test matrix for source/exec/file happy paths + unsupported/imperfect mappings.
- [x] Added package docs with explicit non-goals.
- [x] Added Dockerfile-driven integration tests (`dockerfile2llb` -> `pb.Definition` -> `DefinitionToID`) for simple and complex supported cases.

## Proposed Library Shape (Planning Draft)
- Public entrypoint (exact naming TBD):
  - `func DefinitionToID(def *pb.Definition) (*call.ID, error)`
- Optional debug API (if needed):
  - `func DefinitionToIDWithTrace(def *pb.Definition) (*call.ID, *Trace, error)`
  - `Trace` is diagnostic only, not a fallback mechanism.
- Internal core:
  - DAG parse: `buildkit.DefToDAG(def)`
  - Recursive mapper: `mapOp(dag *buildkit.OpDAG) (*call.ID, error)` with memoization by `(opDigest, outputIndex)`.

## Detailed Implementation Plan (Checklist)

### Phase 1: Package and Contracts
- [x] Create package files:
  - `util/llbtodagger/convert.go`
  - `util/llbtodagger/types.go`
  - `util/llbtodagger/ops_*.go` (split by op class)
  - `util/llbtodagger/convert_test.go`
- [x] Finalize API surface:
  - strict primary API is `(*call.ID, error)`
  - optional trace API may exist for debugging only
- [x] Add package-level docs and explicit non-goals.

### Phase 2: Mapping Strategy Core
- [x] Parse `*pb.Definition` into `*buildkit.OpDAG`.
- [x] Normalize root wrapper ops (top-level synthetic selector op with `Op == nil` case).
- [x] Add memoized recursive conversion of op DAG to call ID DAG.
- [x] Implement deterministic argument builder helpers using `call.NewArgument` + `call.WithArgs`.
- [x] Decide digest policy for synthesized IDs:
  - default recipe digest unless a custom digest is still semantically faithful.
  - if semantics would be lossy/imperfect, return error (do not synthesize).

### Phase 3: Structural Op Mapping
- [x] Implement source op mapping:
  - [x] `docker-image://` -> `query.container(...).from(address: ...)` chain.
  - [x] `git://` -> `query.git(url: ...).<ref/head/...>.tree(...)` chain.
  - [x] `local://` -> `query.host().directory(...)` chain.
  - [x] `http://`/`https://` -> `query.http(url: ...)`.
  - [x] `oci-layout://` currently returns explicit unsupported error.
  - [x] `blob://` is explicitly unsupported; returns error.
- [x] Implement `ExecOp` mapping to container API chain:
  - [x] base/rootfs source mapping
  - [x] mount mapping (`withMountedDirectory`, `withMountedCache`, `withMountedTemp`) for supported cases
  - [x] env/user/cwd argument projection
  - [x] final `withExec(args: ...)` and output mount projection
- [x] Implement `FileOp` mapping:
  - [x] `copy` -> `withDirectory` mapping for supported cases
  - [x] `mkfile` -> `withNewFile`
  - [x] `mkdir` -> `withNewDirectory`
  - [x] `rm` -> `withoutFile`
- [x] Implement `MergeOp` mapping (directory composition chain).
- [x] Implement `DiffOp` mapping (`directory.diff(other: ...)` style chain).
- [x] Implement `BuildOp` behavior:
  - [x] explicitly unsupported; return error immediately.

### Phase 4: Error Handling Framework (Fail-Fast)
- [x] Add stable error categories for unsupported/unfaithful mappings.
- [x] Include op digest + op type in returned errors for diagnosis.
- [x] Ensure converter aborts on first unsupported/imperfect mapping (no fallback path yet).

### Phase 5: Test Matrix
- [x] Unit tests for empty/nil definition behavior.
- [x] Unit tests for each op family using synthetic `llb` definitions.
- [x] Tests that unsupported ops (`BuildOp`, `blob://`) return deterministic errors.
- [x] Tests that imperfect/unfaithful mappings return errors (no fallback).
- [x] Golden tests for deterministic ID encoding across runs.

### Phase 6: Documentation and Developer UX
- [x] Add package `README` or doc comments explaining:
  - supported mappings,
  - explicit unsupported ops,
  - fail-fast behavior.
- [ ] Keep this whiteboard updated after each implementation chunk.

### Phase 6 Nice-To-Have Backlog
- [ ] Add "debug mode" helper to print op->ID mapping trace.

### Phase 7: Dockerfile->LLB Integration Test Matrix
- [x] Add a new dedicated unit test file for Dockerfile-driven conversion coverage.
- [x] Build a helper that runs:
  - Dockerfile bytes -> `dockerfile2llb.Dockerfile2LLB`
  - LLB state -> `Marshal(...).ToPB()`
  - PB definition -> `DefinitionToID`
- [x] Add simple Dockerfile cases (one capability at a time):
  - image source (`FROM ...`)
  - local source (`COPY ...` from main context)
  - exec (`RUN ...`)
  - file ops via Dockerfile instructions (`COPY`, `WORKDIR`, `ENV`)
  - remote HTTP source (`ADD https://...`)
  - remote git source (`ADD https://...git`)
- [x] Add complex Dockerfile cases combining supported features across multiple stages.
- [x] Add deterministic encoding assertions for Dockerfile-driven outputs.
- [ ] Keep this whiteboard updated after each implementation chunk.

## Initial Op Coverage Matrix (Planning Draft)
| LLB op kind | Intended Dagger API representation | Confidence | Status |
|---|---|---|---|
| `SourceOp` docker-image | `query.container().from(address)` (possibly followed by rootfs projection) | High | Implemented |
| `SourceOp` git | `query.git(url...).{head/ref/branch/tag/commit}.tree(...)` | Medium | Implemented |
| `SourceOp` local | `query.host().directory(path, include/exclude/...)` | Low-Medium | Implemented (strict attrs) |
| `SourceOp` http/https | `query.http(url, ...)` | Low-Medium | Implemented (strict attrs) |
| `SourceOp` oci-layout | likely partial (`host.containerImage` or unsupported) | Low | Unsupported (explicit error) |
| `SourceOp` blob | explicitly unsupported -> error | N/A | Unsupported-by-design |
| `ExecOp` | `container.* + withExec(...)` chain | High | Implemented (strict subset) |
| `FileOp` | `directory.with*/without*` / `file.with*` chain | High | Implemented (strict subset) |
| `MergeOp` | directory merge chain | Medium | Implemented |
| `DiffOp` | `directory.diff(other)` | Medium | Implemented |
| `BuildOp` | explicitly unsupported -> error | N/A | Unsupported-by-design |

## Known Snags / Mismatch Log
- `BuildOp` nested build semantics are not supported here; immediate error by policy.
- `local://` source identifiers include session-specific attributes that may not round-trip to a user-level host path.
- `blob://` source is explicitly unsupported here; immediate error by policy.
- Some `ExecOp` mount/metadata details (certain cache/secret/socket internals) may be partially representable only.
- If type/view/module details are missing from LLB context and we cannot represent faithfully, return error (no fallback).
- `dockerfile2llb` coverage note: `COPY --link` currently lowered to FileOp in this test path (not MergeOp), so MergeOp integration is still primarily covered by synthetic LLB tests.

## Current Explicit Unsupported Cases (Implemented)
- All `BuildOp` vertices.
- All `blob://` sources.
- All `oci-layout://` sources (currently unsupported).
- `ExecOp` with non-default network/security, secret env, secret/ssh mounts, readonly bind mounts, non-default mount content cache, or unsupported metadata fields.
- `FileOp` copy actions with mode override, archive auto-unpack, `alwaysReplaceExistingDestPaths`, or `createDestPath=false`.
- `FileOp` mkdir without `makeParents=true`.
- `FileOp` mkfile with non-UTF8 content.
- Named user/group ownership (`byName`) in file actions.

## Crucial Notes To Not Forget
- No custom-op handling in this package.
  - Ignore `dagger.customOp`.
  - Do not decode/convert `dagop.fs`, `dagop.raw`, `dagop.ctr`.
- Unsupported/unfaithful mapping policy is strict fail-fast for now.
  - First unsupported/imperfect mapping returns error immediately.

## Whiteboard Usage Rules (For This Task)
- Every time scope, assumptions, or mapping behavior changes, update this file in the same change.
- For each implemented op mapper, update:
  - checklist box,
  - coverage matrix status,
  - mismatch log (if new gaps found).
- Keep this file current across context compaction; treat it as the source-of-truth progress ledger.
