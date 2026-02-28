# LLB -> Dagger ID Whiteboard

Last updated: 2026-02-28

## Explicit Goal and Hard Requirements (Do Not Forget)
- Goal: implement a utility library under `./util/llbtodagger` that converts BuildKit LLB + Docker image metadata into a Dagger `*call.ID`.
- Required input/output:
  - Input: `*pb.Definition` (BuildKit LLB definition).
  - Input: `*dockerspec.DockerOCIImage` (Docker OCI image metadata/config, e.g. from `dockerfile2llb`).
  - Output: `*call.ID` (must reference Dagger API calls/fields).
  - Output object type should be `Container` for Docker-build-derived LLB.
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
- For Docker-build-derived LLB, prefer returning a `Container`-typed ID and preserve container metadata where representable from LLB.

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
- [x] Updated converter/tests so final IDs are container-typed for Docker-build-derived outputs.
- [x] Updated converter/tests so Dockerfile metadata is applied via image-config input where representable.
- [x] Added `core/integration` end-to-end suite validating Dockerfile -> dockerfile2llb -> DefinitionToID -> LoadContainerFromID execution path.

## Proposed Library Shape (Planning Draft)
- Public entrypoint (exact naming TBD):
  - `func DefinitionToID(def *pb.Definition, img *dockerspec.DockerOCIImage) (*call.ID, error)`
    - pass `img=nil` when metadata input is unavailable.
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
- [x] Keep this whiteboard updated after each implementation chunk.

### Phase 6 Nice-To-Have Backlog
- [ ] Add "debug mode" helper to print op->ID mapping trace.

### Phase 7: Dockerfile->LLB Integration Test Matrix
- [x] Add a new dedicated unit test file for Dockerfile-driven conversion coverage.
- [x] Build a helper that runs:
  - Dockerfile bytes -> `dockerfile2llb.Dockerfile2LLB`
  - LLB state -> `Marshal(...).ToPB()`
  - PB definition + Docker image config -> `DefinitionToID`
- [x] Add simple Dockerfile cases (one capability at a time):
  - image source (`FROM ...`)
  - local source (`COPY ...` from main context)
  - exec (`RUN ...`)
  - file ops via Dockerfile instructions (`COPY`, `WORKDIR`, `ENV`)
  - remote HTTP source (`ADD https://...`)
  - remote git source (`ADD https://...git`)
- [x] Add complex Dockerfile cases combining supported features across multiple stages.
- [x] Add deterministic encoding assertions for Dockerfile-driven outputs.
- [x] Keep this whiteboard updated after each implementation chunk.

### Phase 8: Container-Typed Output and Metadata Preservation
- [x] Update conversion flow so final `DefinitionToID` output is container-typed for Docker-build LLB.
- [x] Introduce two-input conversion API (`pb.Definition` + `DockerOCIImage`) for metadata-complete conversion.
- [x] Preserve metadata where representable from Docker OCI config (entrypoint/cmd/env/user/workdir/labels/exposed ports).
- [x] Update unit tests to assert container IDs and metadata-sensitive fields.
- [x] Document metadata not inferable from `pb.Definition` alone.

### Phase 9: End-to-End Integration Tests (Core Integration Suite)
- [x] Add a new integration test file under `./core/integration` with suite name `LLBToDaggerSuite`.
- [x] Add helper pipeline in integration tests:
  - Dockerfile string -> `dockerfile2llb.Dockerfile2LLB`
  - LLB state -> `Marshal(...).ToPB()`
  - PB definition + Docker image config -> `llbtodagger.DefinitionToID`
  - ID encode -> SDK load path
- [x] Add execution checks that exercise loaded objects (not only IDs):
  - call `Sync` and/or `WithExec`/`Stdout`
  - inspect files/contents produced by Dockerfile instructions
  - validate expected side effects from `RUN`, `COPY`, `ADD`, `WORKDIR`, `ENV`
- [x] Add at least one complex multi-stage Dockerfile end-to-end case.
- [x] Add deterministic checks for ID encoding in integration path where stable.
- [x] Keep this whiteboard updated after each implementation chunk.

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
- Container output nuance is resolved:
  - `DefinitionToID` now returns a container-typed ID for Dockerfile-derived paths.
  - Metadata calls may be appended after `withRootfs`/`withExec`, so tests should not assume `withRootfs` is the terminal field.
- Metadata inference limit resolved by API change:
  - `dockerfile2llb` returns final image config (`img`) separately from `*pb.Definition`.
  - Converter should accept this as a second input for metadata-complete output.
- Image config nuance:
  - `Config.ArgsEscaped` is ignored on non-Windows images (no Dagger API equivalent needed for Linux behavior).
  - `Config.ArgsEscaped` on Windows images is still unsupported and returns error.

## Current Explicit Unsupported Cases (Implemented)
- All `BuildOp` vertices.
- All `blob://` sources.
- All `oci-layout://` sources (currently unsupported).
- `ExecOp` with non-default network/security, secret env, secret/ssh mounts, readonly bind mounts, non-default mount content cache, or unsupported metadata fields.
- `FileOp` copy actions with mode override, archive auto-unpack, `alwaysReplaceExistingDestPaths`, or `createDestPath=false`.
- `FileOp` mkdir without `makeParents=true`.
- `FileOp` mkfile with non-UTF8 content.
- Named user/group ownership (`byName`) in file actions.

## Detailed Unsupported Nuance Catalog (Exhaustive, Dockerfile-Classified)

### Unsupported and relevant to Dockerfile-generated LLB
- Platform `OSVersion` and `OSFeatures` are unsupported.
- Local source `followPaths` is unsupported.
- HTTP checksum enforcement attr is unsupported (`ADD --checksum` path).
- Non-default network mode is unsupported (`RUN --network=...`).
- Non-sandbox security mode is unsupported (`RUN --security=...` when enabled).
- Secret environment variables are unsupported (`RUN --mount=type=secret,env=...`).
- Proxy environment injection is unsupported (proxy build-arg path).
- Read-only bind mounts are unsupported (`RUN --mount=type=bind,readonly`).
- Secret mounts are unsupported (`RUN --mount=type=secret`).
- SSH mounts are unsupported (`RUN --mount=type=ssh`).
- `copy` mode override is unsupported (`COPY/ADD --chmod` path).
- `copy` archive auto-unpack compatibility mode is unsupported (`ADD` local archive path).
- Group-only `chown` is unsupported.
- Named user/group `chown` (`byName`) is unsupported.
- Empty named user in `chown` is unsupported.
- Healthcheck metadata is unsupported.
- ONBUILD metadata is unsupported.
- Shell metadata is unsupported.
- Volumes metadata is unsupported.
- Stop signal metadata is unsupported.
- `ArgsEscaped` on Windows images is unsupported.

### Unsupported but outside Dockerfile instruction support (or malformed/non-canonical LLB)
- Synthetic root op with more than one input is rejected.
- Unknown/non-classified op types are rejected.
- Top-level non-Directory/non-Container result types are rejected.
- Any non-Directory/non-Container input where a Directory is required is rejected.
- Source identifiers with wrong or empty scheme payload are rejected.
- Platform with missing `OS` or `Architecture` is rejected.
- `BuildOp` is explicitly unsupported.
- `blob://` source ops are explicitly unsupported.
- `oci-layout://` source ops are currently unsupported.
- Unknown source schemes are unsupported.
- Git source missing remote URL is rejected.
- Git custom auth token secret names are unsupported (only default secret key accepted).
- Git custom auth header secret names are unsupported (only default secret key accepted).
- Git known-hosts override is unsupported.
- Git custom SSH socket mount names are unsupported (only default/empty accepted).
- Any unrecognized git source attr is unsupported.
- Local source with empty resolved path is rejected.
- Local source invalid include/exclude JSON attrs are rejected.
- Local source non-metadata differ mode is unsupported.
- Any unrecognized local source attr is unsupported.
- HTTP source with non-HTTP(S) identifier is rejected.
- Any unrecognized HTTP source attr is unsupported.
- HTTP uid/gid override attrs are unsupported.
- HTTP invalid URL parsing is rejected.
- HTTP invalid permission attr parsing is rejected.
- Missing exec op or missing exec meta is rejected.
- Hostname override is unsupported.
- Extra hosts are unsupported.
- Ulimit settings are unsupported.
- Cgroup parent is unsupported.
- Valid-exit-code overrides are unsupported.
- Missing root bind mount (`dest="/"`, bind type) is unsupported.
- Mount `resultID` usage is unsupported.
- Non-default mount content cache mode is unsupported.
- Cache mounts without cache ID are unsupported.
- Cache sharing modes outside `{SHARED, PRIVATE, LOCKED}` are unsupported.
- Any unknown mount type is unsupported.
- Invalid env entries without `name=value` are rejected.
- Missing output mount for selected output index is unsupported.
- Mount input indices out of range are unsupported.
- Missing merge/diff/file op payloads are rejected.
- Diff input indices out of range are unsupported.
- Missing output mapping for selected file output index is unsupported.
- File action input indices out of range are unsupported.
- File action references to unresolved prior action outputs are unsupported.
- Primary file action input must be a Directory; other types are unsupported.
- File copy secondary input must be a Directory; other types are unsupported.
- File actions other than `mkdir`, `mkfile`, `rm`, `copy` are unsupported.
- `mkdir` without `makeParents=true` is unsupported.
- `mkdir` timestamp override is unsupported.
- `mkfile` timestamp override is unsupported.
- `mkfile` non-UTF8/binary payload is unsupported.
- `copy` `alwaysReplaceExistingDestPaths=true` is unsupported.
- `copy` with `createDestPath=false` is unsupported.
- Unknown `UserOpt` discriminator in `chown` is unsupported.
- Invalid env entries (without `name=value`) are rejected.
- Exposed ports with invalid format are rejected.
- Exposed ports outside `1..65535` are rejected.
- Exposed ports using protocols other than TCP/UDP are unsupported.

## Crucial Notes To Not Forget
- `skills/cache-expert/references/debugging.md` is the authoritative source for how to run integration tests; follow it exactly.
  - This applies to both primary agent commands and any subagent that runs/tests/parses integration output.
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
