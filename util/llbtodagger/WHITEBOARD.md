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
- [x] Added readonly bind-mount support + non-sticky exec mount cleanup semantics for converted `ExecOp` mount handling.
- [x] Added support for `copy` to explicit file destination paths by mapping single-file copies to `withFile`.
- [x] Added support for group-only `chown` (`--chown=:gid`) in converted file ops.
- [x] Added support for named user/group `chown` on copy actions when container context is available.
- [x] Added internal container metadata mapping for Docker image config fields: healthcheck, onbuild, shell, volumes, stop signal.

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
- [x] Add export-based metadata verification in e2e coverage:
  - [x] export resulting container image (local temp dir or test registry path),
  - [x] inspect OCI image config (`config` JSON),
  - [x] assert expected metadata fields are present/unchanged (especially metadata not directly observable via runtime exec behavior).
- [x] Keep this whiteboard updated after each implementation chunk.

### Phase 10: `withDirectory` Permissions Support (`copy` mode override)
- [x] Extend engine API to support explicit permissions on directory copy operations.
  - [x] Add `permissions` argument to `Directory.withDirectory`.
  - [x] Add `permissions` argument to `Container.withDirectory` for API parity.
  - [x] Thread the new argument through schema args -> core methods -> copy implementation.
- [x] Add dedicated integration coverage in `core/integration/directory_test.go`:
  - [x] Verify `Directory.withDirectory(..., permissions: ...)` applies mode recursively to copied tree.
  - [x] Keep coverage separate from `LLBToDaggerSuite`.
- [x] Add `LLBToDaggerSuite` integration coverage for `COPY --chmod`:
  - [x] dedicated `COPY --chmod` case.
  - [x] complex multi-op Dockerfile case also validates chmod-preserved output permissions.
- [x] Update `util/llbtodagger` copy mapping:
  - [x] Map `pb.FileActionCopy.Mode` to the new `withDirectory(permissions: ...)` API instead of erroring.
- [x] Regenerate Go SDK after schema API changes.
  - [x] Required command: `dagger -y call -m ./toolchains/go-sdk-dev generate`
- [x] Update unsupported catalogs/checklists to remove `copy mode override` once implemented.

### Phase 11: Read-Only Bind Mount Support + Non-Sticky Exec Mount Semantics
- [x] Extend container API for read-only directory mounts.
  - [x] Add optional `readOnly` (or `readonly`, schema-consistent naming) argument to `Container.withMountedDirectory`.
  - [x] Thread arg through schema -> core `WithMountedDirectory(..., readonly bool)` call.
  - [x] Regenerate Go SDK after schema change (`dagger -y call -m ./toolchains/go-sdk-dev generate`; use `--workspace=.` in this worktree if needed).
- [x] Update llbtodagger `ExecOp` bind-mount conversion for read-only mounts.
  - [x] Remove fail-fast rejection for `m.Readonly` bind mounts.
  - [x] Map `pb.Mount{MountType=BIND, Readonly=true}` to `withMountedDirectory(..., readOnly: true)`.
- [x] Enforce BuildKit-style mount lifetime semantics in converter output.
  - [x] Ensure mounts introduced for an `ExecOp` do not leak/stick into subsequent execs.
  - [x] Add `withoutMount(path: ...)` cleanup in the generated chain where needed so post-exec state matches BuildKit behavior.
  - [x] Keep ordering deterministic (mount application, exec, cleanup, and output projection ordering).
- [x] Add/adjust unit tests in `util/llbtodagger`.
  - [x] Replace readonly-bind "unsupported" expectation with positive mapping assertions.
  - [x] Add a multi-exec synthetic LLB test that fails if mount stickiness leaks across exec boundaries.
  - [x] Assert generated ID chain contains expected `withMountedDirectory` + cleanup structure for sticky/non-sticky semantics.
- [x] Add integration coverage.
  - [x] Add llbtodagger Dockerfile-driven integration case for `RUN --mount=type=bind,readonly` conversion/runtime behavior.
  - [x] Add llbtodagger Dockerfile-driven integration case with two RUN steps: mount used in first RUN, then explicitly absent in second RUN (non-sticky check).
  - [x] Add direct container API integration coverage (outside llbtodagger) validating `withMountedDirectory(readOnly: true)` behavior.
- [x] Validation + whiteboard bookkeeping.
  - [x] Run focused unit tests: `go test ./util/llbtodagger`.
  - [x] Run focused integration tests using the debugging.md-prescribed workflow.
  - [x] Remove "readonly bind mounts unsupported" entries from unsupported catalogs after implementation lands.

### Phase 12: Group-Only `chown` Support
- [x] Support group-only ownership mapping in llbtodagger file actions.
  - [x] Map group-only chown to Dagger owner string using explicit UID (`0:<gid>` baseline) instead of erroring.
  - [x] Preserve current fail-fast behavior for unsupported named-user/group variants that still cannot be represented.
- [x] Add unit coverage.
  - [x] Add focused tests for chown-owner normalization helper behavior for group-only inputs.
  - [x] Add FileOp conversion unit assertions for group-only chown on supported copy paths.
- [x] Add Dockerfile-driven conversion unit coverage.
  - [x] Add `COPY --chown=:<gid>` conversion test and assert owner field in emitted ID chain.
- [x] Add integration coverage.
  - [x] Add llbtodagger integration test using Dockerfile with group-only chown and assert resulting uid:gid on copied artifact.
- [x] Validation + catalog updates.
  - [x] Run `go test ./util/llbtodagger`.
  - [x] Run focused `core/integration` llbtodagger tests via debugging.md command shape.
  - [x] Remove/update "group-only chown unsupported" entries after implementation lands.

### Phase 13: Named User/Group `chown` via Container-Aware FileOp Mapping
- [x] Planning + guardrails.
  - [x] Keep fail-fast behavior for any named-ownership case that cannot be represented faithfully.
  - [x] Scope to Dockerfile-relevant `COPY/ADD --chown=<name>` path first; do not broaden with fallback heuristics.
- [x] Converter support.
  - [x] Accept non-empty `UserOpt_ByName` values in `chownOwnerString` normalization.
  - [x] Detect owner strings that require name resolution (`user/group` names vs numeric ids).
  - [x] For copy actions with named ownership, emit container-level `withFile` / `withDirectory` calls (with rootfs sync) so Dagger resolves names through container `/etc/passwd` and `/etc/group`.
  - [x] Keep numeric/group-only paths on the existing directory-level fast path.
  - [x] Return explicit unsupported errors when named ownership is requested but no container context is available.
- [x] Tests.
  - [x] Add unit tests for owner normalization that include named-user and named-group cases.
  - [x] Add FileOp conversion tests asserting named-chown copy emits container-level calls.
  - [x] Add Dockerfile-driven conversion tests for `COPY --chown=<name>`.
  - [x] Add integration tests that create users/groups in image and assert final file ownership after `LoadContainerFromID`.
- [x] Validation + bookkeeping.
  - [x] Run `go test ./util/llbtodagger`.
  - [x] Run focused `core/integration` llbtodagger tests following `skills/cache-expert/references/debugging.md`.
  - [x] Update unsupported catalogs to remove named-chown entries that are now supported and keep any still-unsupported nuances explicit.

### Phase 14: Internal Container API for OCI Metadata Fields
- [x] Add internal-only container schema API (underscore-prefixed) for setting OCI config metadata not currently exposed in public SDK methods.
  - [x] Confirm API naming/shape (`_...`) so it is callable from raw ID construction but intentionally not codegen'd for SDKs.
  - [x] Keep scope limited to llbtodagger conversion needs; avoid broad public-surface changes.
- [x] Add arguments covering the currently unsupported Dockerfile-relevant metadata:
  - [x] `healthcheck`
  - [x] `onBuild`
  - [x] `shell`
  - [x] `volumes`
  - [x] `stopSignal`
- [x] Define GraphQL-friendly argument encodings for non-scalar fields.
  - [x] Representation for `healthcheck`: JSON-encoded `dockerspec.HealthcheckConfig`.
  - [x] Representation for `volumes`: sorted `[]string` of volume paths (re-hydrated to map/set in schema resolver).
- [x] Thread schema args into core container mutation logic and ensure `Container.Config` is updated deterministically.
- [x] Keep existing behavior unchanged unless internal API is explicitly used.
- [x] Regenerate Go SDK if schema generation is impacted (even though underscore APIs are internal-only).
  - [x] Command: `dagger -y call -m ./toolchains/go-sdk-dev generate`
- [x] Update llbtodagger metadata conversion to use the new internal API instead of fail-fast for these fields.
- [x] Add/expand tests:
  - [x] unit tests for the internal schema resolver path and config mutation behavior.
  - [x] llbtodagger unit tests that verify IDs include internal metadata call when these fields are present.
  - [x] integration test coverage (llbtodagger and/or container-focused) validating loaded container behavior/config where observable.
  - [x] include export-and-inspect assertions for OCI config fields (healthcheck/onbuild/shell/volumes/stopSignal) so metadata is validated directly, not only via runtime behavior.
- [x] Update unsupported catalogs after implementation lands, removing items no longer unsupported.

### Phase 15: Hard-Cutover `dockerBuild` Integration
- [x] Scope and entrypoints.
  - [x] Cut over `directory.dockerBuild` to the new LLB->ID pipeline while keeping API shape unchanged.
  - [x] Cut over deprecated `container.build` via the same `Container.Build` implementation path.
- [x] Replace `Container.Build` internals with llbtodagger pipeline.
  - [x] Read Dockerfile bytes from `dockerfileDir`.
  - [x] Convert `contextDir.LLB` to `llb.State` and call `dockerfile2llb.Dockerfile2LLB`.
  - [x] Marshal returned state to `*pb.Definition`.
  - [x] Convert definition+image metadata to `*call.ID` via `llbtodagger.DefinitionToID`.
  - [x] Load resulting `ContainerID` through DAGQL/server load path and return resulting `*core.Container`.
- [x] Preserve old implementation as commented reference blocks in `Container.Build` for this transition only.
  - [x] Comment out legacy `bk.Solve` path.
  - [x] Comment out legacy `WithSecretTranslator` / `WithSSHTranslator` setup.
  - [x] Comment out legacy local `DefToDAG` walk/marshal mutation path.
  - [x] Put this exact TODO above each commented block:
    - [x] `TODO: remove commented code once fully replaced, just a reference on how it used to work for now`
- [x] Deliberate first-iteration behavior (accepted regressions for now).
  - [x] Secret/SSH dockerBuild tests are expected to fail in this phase; do not block cutover on them.
  - [x] Do not implement secret handling in this phase.
  - [x] Do not implement SSH handling in this phase.
  - [x] Do not support remote frontend syntax pragma behavior (`#syntax=...`) in this phase.
- [ ] Follow-up TODOs to keep visible for next iteration.
  - [ ] TODO: restore/port `noInit` behavior in cutover path.
  - [ ] TODO: implement secret mount/env support in llbtodagger exec conversion.
  - [ ] TODO: implement SSH mount support in llbtodagger exec conversion.
  - [ ] TODO: decide and implement SSH ID mapping semantics.
- [x] Validation and bookkeeping.
  - [x] Run focused dockerBuild integration tests (expect secret/ssh failures in this phase).
  - [x] Keep `WHITEBOARD.md` updated with exactly which dockerBuild tests fail post-cutover.
  - 2026-02-28 focused runs:
    - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDockerfile/TestDockerBuild'` -> fail
    - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDockerfile/TestBuildMergesWithParent'` -> pass
  - Failures observed in `TestDockerfile/TestDockerBuild`:
    - `with_syntax_pragma`, `with_old_syntax_pragma`: expected for Phase 15 (`#syntax=...` unsupported in cutover path).
    - secret/ssh cases (`with_build_secrets*`, `with_unknown_build_secrets*`, `TestDockerBuildSSH/*`): expected for Phase 15 (secret/ssh unsupported).
    - many baseline copy-path cases (`default_Dockerfile_location`, custom/subdirectory Dockerfile location, `.dockerignore` compatibility, `with_build_args`, `prevent_duplicate_secret_transform`) failed due:
      - `llbtodagger: unsupported op "file.copy": copy without createDestPath is unsupported`.
    - This `file.copy createDestPath=false` gap was the major non-secret/non-ssh blocker for broad dockerBuild cutover coverage (resolved in Phase 16).

### Phase 16: `file.copy createDestPath=false` Support
- Goal:
  - Support BuildKit `FileActionCopy.CreateDestPath=false` semantics in llbtodagger conversion.
  - Unblock dockerBuild cutover tests currently failing on this unsupported copy variant.
- Semantics to preserve (BuildKit reference behavior):
  - When `createDestPath=false`, copy should fail if destination parent path does not exist.
  - BuildKit checks destination parent existence before copy (`internal/buildkit/solver/llbsolver/file/backend.go`, `docopy`).
- Initial gap (before this phase):
  - llbtodagger errored on all `createDestPath=false`.
  - Existing Dagger `Directory.withDirectory` / `Directory.withFile` implementations always created parent directories (`MkdirAll` path), so they could not express `createDestPath=false` faithfully.

- Decision:
  - Selected approach: Option B (full-fidelity, internal hidden args).

- Option B (selected):
  - Add hidden internal args (`internal:"true"`) to existing copy schema args using inverted naming: `doNotCreateDestPath`.
  - Keep default `doNotCreateDestPath=false` so current Dagger behavior (create destination parent paths) remains unchanged.
  - Wire llbtodagger to set `doNotCreateDestPath=true` via raw ID construction when LLB has `createDestPath=false`.
  - Keep public SDK surface unchanged (internal APIs callable via raw ID only).
  - Rationale: faithful semantics and broad dockerBuild compatibility without changing public SDK behavior.

- Option B execution plan:
  - [x] 16.1 Add hidden internal args (`internal:"true"`) to existing directory/container copy schema args:
    - [x] `doNotCreateDestPath bool` with default `false`.
  - [x] 16.2 Implement core behavior:
    - [x] when `doNotCreateDestPath=false`, keep current behavior (create destination parent path as today).
    - [x] when `doNotCreateDestPath=true`, verify destination parent exists and return error if missing.
  - [x] 16.3 Update llbtodagger `applyCopy`:
    - [x] map `CreateDestPath=true` as today.
    - [x] map `CreateDestPath=false` by setting hidden internal arg `doNotCreateDestPath=true` in call IDs.
  - [x] 16.4 Tests:
    - [x] unit: ID construction for `createDestPath=false` copy path.
    - [x] integration: successful copy when parent exists.
    - [x] integration: expected error when parent missing.
    - [x] rerun focused dockerBuild integration tests from debugging.md command shape.
  - [x] 16.5 Bookkeeping:
    - [x] update unsupported catalog entry for `createDestPath=false` after landing.
  - 2026-02-28 validation runs:
    - `dagger -y call -m ./toolchains/go-sdk-dev generate` -> pass (`no changes to apply`)
    - `go test ./util/llbtodagger ./core ./core/schema` -> pass
    - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestLLBToDagger/TestLoadContainerFromConvertedIDCopyDoNotCreateDestPath'` -> pass
    - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestLLBToDagger'` -> pass
    - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDockerfile/TestDockerBuild'` -> fail (expected for known unsupported areas); `createDestPath=false unsupported` error is gone.
      - Remaining failures include syntax pragma, secret/ssh, and several context-copy cases now failing with missing-path errors (for example `stat /src: no such file or directory`, `stat /subcontext: no such file or directory`).
      - Empirical debug (`TestDockerfile/TestDockerBuild/default_Dockerfile_location`):
        - failure now occurs while loading converted ID at `directory(path: "/src")` inside source resolution (`stat /src: no such file or directory`).
        - this `directory(path: "/src")` comes from `Directory.StateWithSourcePath()` copy-shim (`llb.Copy(dirSt, dir.Dir, ".", CopyDirContentsOnly:true)` with `dir.Dir=/src`).
        - indicates current local/context conversion path is still mismatched for these dockerBuild contexts; after removing prior `createDestPath=false` blocker, this is the next concrete failure to address.

### Phase 17: Sentinel `MainContext` + Structural Context Rebinding (No `Directory.State*` Dependency)
- Goal:
  - Remove `dockerBuild` reliance on `Directory.State()` / `Directory.StateWithSourcePath()` for Dockerfile context injection.
  - Preserve Dockerfile2LLB's path/selector semantics while binding context reads to the real Dagger directory ID.
  - Fix current `stat /src` / `stat /subcontext` class failures without text-based ID hacks.
- Approach:
  - Feed `dockerfile2llb` a synthetic, deterministic sentinel `MainContext` local source state.
  - Extend `llbtodagger` conversion so sentinel local-source ops are rebound to the actual Dagger context directory ID provided by caller.
  - Perform replacement structurally in ID construction/conversion logic; never do string replacement on encoded/display IDs.

- Checklist:
  - [x] Define sentinel local-source identity constants (name/shared-key marker) in the dockerBuild conversion path.
  - [x] Build sentinel `llb.State` for `ConvertOpt.MainContext` (valid non-nil output graph; deterministic marker).
  - [x] Update `core.Container.Build` to stop calling `contextDir.State*` for Dockerfile2LLB `MainContext`.
  - [x] Extend llbtodagger API to accept context-rebinding input (actual Dagger context `Directory` ID/call ID) alongside `def + img`.
  - [x] Add strict validation: if sentinel local source appears in LLB and no context rebinding value is provided, return fail-fast error.
  - [x] Implement sentinel detection in local-source conversion (identifier + attrs match), and map it to provided context directory ID.
  - [x] Keep non-sentinel local-source behavior unchanged.
  - [x] Ensure selector/include/exclude/copy semantics emitted by Dockerfile2LLB still apply on top of rebound context directory.
  - [x] Add llbtodagger unit tests:
    - [x] sentinel local source maps to provided directory ID.
    - [x] missing rebinding input for sentinel returns explicit error.
    - [x] sentinel marker does not leak into final emitted ID display/encoding.
  - [x] Add/adjust dockerBuild integration coverage:
    - [x] `TestDockerfile/TestDockerBuild/default_Dockerfile_location` passes.
    - [x] `subdirectory_with_default_Dockerfile_location` passes.
    - [x] custom Dockerfile location variants pass.
  - [x] Re-run focused integration suite using debugging.md command shape and log outcomes in this whiteboard.

- 2026-02-28 validation runs:
  - `go test ./util/llbtodagger ./core ./core/schema` -> pass.
  - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDockerfile/TestDockerBuild/default_Dockerfile_location'` -> pass.
  - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDockerfile/TestDockerBuild/(custom_Dockerfile_location|subdirectory_with_default_Dockerfile_location|subdirectory_with_custom_Dockerfile_location)'` -> pass.
  - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDockerfile/TestDockerBuild'` -> fail (expected+known unsupported plus one newly surfaced copy-semantics issue):
    - expected unsupported: ssh mounts, secret mounts, remote syntax pragma, builtin secret-mount exec path.
    - newly surfaced non-context issue (resolved): `onbuild_command_is_published` exposed COPY source-missing behavior mismatch (include-filter no-op vs BuildKit error).
  - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDockerfile/TestDockerBuild/onbuild_command_is_published'` -> pass after strict source-path existence wiring (`requiredSourcePath` internal arg on `withDirectory` path).

- Follow-up tie-in (future branch):
  - This phase is the prerequisite for removing `Directory.State()` usage from dockerBuild path in the branch where LLB is no longer a general Dagger runtime dependency.

### Phase 18: Secret Env + Secret Mount Support (dockerBuild Cutover)
- Goal:
  - Support Dockerfile/LLB secret environment and secret mount semantics in hard-cutover `dockerBuild` through llbtodagger conversion.
  - Keep strict fail-fast behavior for unsupported/imperfect cases.
- Scope:
  - In scope: `ExecOp.Secretenv` and `ExecOp` mounts with `MountType_SECRET`.
  - Out of scope for this phase: `MountType_SSH` (stays unsupported until a dedicated follow-up phase).

- Design checklist:
  - [x] Extend llbtodagger conversion options with secret resolution input.
    - [x] Add `DefinitionToIDOptions` field for mapping LLB secret IDs -> Dagger `Secret` call IDs.
    - [x] Keep converter independent from `core` package types; use `*call.ID` mapping data only.
  - [x] Wire dockerBuild secret inputs into llbtodagger options in `core.Container.Build`.
    - [x] Build name->secret-ID mapping from `secrets []dagql.ObjectResult[*Secret]` + `secretStore.GetSecretName(...)`.
    - [x] Pass map into `DefinitionToIDWithOptions(...)`.
  - [x] Implement `ExecOp.Secretenv` conversion.
    - [x] Map to `container.withSecretVariable(name: ..., secret: ...)`.
    - [x] Required/optional behavior:
      - [x] If secret ID is mapped: emit `withSecretVariable`.
      - [x] If secret ID missing and `optional=true`: skip.
      - [x] If secret ID missing and `optional=false`: return explicit error.
  - [x] Implement `MountType_SECRET` conversion.
    - [x] Map to `container.withMountedSecret(path: ..., source: ..., owner: ..., mode: ...)`.
    - [x] Map owner from `uid/gid` to owner string (`<uid>:<gid>`).
    - [x] Preserve file mode semantics via `mode` arg.
    - [x] Required/optional behavior:
      - [x] If secret ID is mapped: emit `withMountedSecret`.
      - [x] If secret ID missing and `optional=true`: skip.
      - [x] If secret ID missing and `optional=false`: return explicit error.
  - [x] Preserve BuildKit per-RUN non-sticky behavior inside converted exec chains.
    - [x] Secret envs are cleaned with `withoutSecretVariable`.
    - [x] Secret mounts are cleaned with `withoutMount(path)`.
    - [x] `core.Container.WithoutMount` also removes secret mounts with matching `MountPath`.
  - [x] Keep deterministic cleanup ordering and dedupe cleanup entries.
    - [x] Stable order for secret env + mount cleanup calls.
    - [x] Duplicate cleanup emission avoided.
  - [x] Preserve legacy dockerBuild returned-container secret behavior.
    - [x] After loading converted container ID, clone and append named secret mounts on `container.Secrets` (legacy-compatible persistence on returned container object).
    - [x] Clone-before-mutate to avoid cache object corruption.

- Test checklist:
  - [x] llbtodagger unit tests (`util/llbtodagger/convert_test.go`):
    - [x] Secret env mapping emits `withSecretVariable` + cleanup.
    - [x] Secret mount mapping emits `withMountedSecret` + cleanup.
    - [x] Missing required secret returns explicit error.
    - [x] Missing optional secret is skipped (no emitted secret calls).
    - [x] Multiple secrets in one exec preserve deterministic ordering.
  - [x] Dockerfile-driven converter tests (`util/llbtodagger/dockerfile_convert_test.go`):
    - [x] `RUN --mount=type=secret,id=...,env=...` converts as expected.
    - [x] `RUN --mount=type=secret,id=...` mount path/mode/owner mapping.
    - [x] Optional unknown secret does not fail conversion path.
    - [x] Required unknown secret yields explicit conversion error.
  - [x] Core integration tests (`core/integration`):
    - [x] Add/extend llbtodagger e2e test in `llbtodagger_test.go` for secret env + secret mount behavior.
    - [x] Re-run `TestDockerfile/TestDockerBuild` secret cases and make builtin frontend path pass:
      - [x] `with_build_secrets`
      - [x] `with_unknown_build_secrets`
    - [x] Add explicit non-sticky validation across two RUNs for secret mount/env.
    - [x] Keep `#syntax=...` remote frontend expectations unchanged for now (still unsupported by cutover path).
  - [x] Validation command shape:
    - [x] Follow `skills/cache-expert/references/debugging.md` for integration test runs.
    - [x] Use sub-agents for longer integration test execution/log parsing when iterating.

- Completion notes:
  - `with_build_secrets` and `with_unknown_build_secrets` now pass on dockerBuild hard-cutover path.
  - Converted `RUN --mount=type=secret`/`env=...` behavior is non-sticky per-run via cleanup calls.
  - Returned container keeps legacy-compatible named secret entries for dockerBuild output behavior.

- Post-landing bookkeeping:
  - [x] Remove secret-env/secret-mount entries from `Unsupported and relevant to Dockerfile-generated LLB`.
  - [x] Keep SSH unsupported entry in place until SSH phase lands.
  - [x] Update `Current Explicit Unsupported Cases` and nuanced catalogs accordingly.

### Phase 19: SSH Socket Mount Support (dockerBuild Cutover)
- Goal:
  - Support Dockerfile/LLB SSH socket mount semantics (`RUN --mount=type=ssh`) in hard-cutover `dockerBuild` through llbtodagger conversion.
  - Keep fail-fast behavior for unsupported/imperfect cases.
- Scope:
  - In scope: `ExecOp` mounts with `MountType_SSH`.
  - Out of scope for this phase: git source custom SSH socket naming nuances beyond current Dockerfile path requirements.

- Design checklist:
  - [x] Extend llbtodagger conversion options with SSH socket resolution input.
    - [x] Add `DefinitionToIDOptions` field mapping LLB SSH IDs -> Dagger `Socket` call IDs.
    - [x] Keep converter independent from `core` package types; mapping stays `*call.ID`.
  - [x] Wire dockerBuild SSH input into llbtodagger options in `core.Container.Build`.
    - [x] Remove current hard error `dockerBuild SSH mounts are not supported...`.
    - [x] Map dockerBuild SSH argument to LLB SSH IDs expected by Dockerfile frontend (including default/empty ID path via fallback mapping).
    - [x] Define and implement mapping semantics for non-default SSH IDs with single SSH input: empty-key mapping acts as default for any unmatched SSH ID (legacy translator-compatible behavior).
  - [x] Implement `MountType_SSH` conversion in `util/llbtodagger/exec.go`.
    - [x] Map to `container.withUnixSocket(path: ..., source: ..., owner: ...)`.
    - [x] Map owner from `uid/gid` to owner string (`<uid>:<gid>`).
    - [x] Required/optional behavior:
      - [x] If socket ID is mapped: emit `withUnixSocket`.
      - [x] If socket ID missing and `optional=true`: skip.
      - [x] If socket ID missing and `optional=false`: return explicit error.
  - [x] Preserve BuildKit per-RUN non-sticky behavior for SSH mounts.
    - [x] Cleanup converted exec chain with `withoutUnixSocket(path)` after `withExec`.
    - [x] Keep cleanup deterministic and deduped.
  - [x] Decide on SSH mount mode handling.
    - [x] Current `withUnixSocket` API has no explicit mode control.
    - [x] For now, fail fast on unsupported non-default mode combinations.
  - [x] Preserve cache safety when mutating loaded container objects (clone before mutation where needed).

- Test checklist:
  - [x] llbtodagger unit tests (`util/llbtodagger/convert_test.go`):
    - [x] SSH mount mapping emits `withUnixSocket` + cleanup.
    - [x] Missing required SSH socket returns explicit error.
    - [x] Missing optional SSH socket is skipped.
    - [x] Deterministic ordering/dedupe for repeated SSH mounts.
  - [x] Dockerfile-driven converter tests (`util/llbtodagger/dockerfile_convert_test.go`):
    - [x] `RUN --mount=type=ssh` converts as expected.
    - [x] Optional unknown SSH ID does not fail conversion path.
    - [x] Required unknown SSH ID yields explicit conversion error.
  - [x] Core integration tests (`core/integration`):
    - [x] Re-enable/target `TestDockerBuildSSH/*` coverage on cutover path.
    - [x] Add llbtodagger integration coverage in `llbtodagger_test.go` for SSH mount behavior + non-sticky cleanup.
    - [x] Validate mounted SSH socket behavior functionally (socket available during mounted RUN, absent in next RUN).
  - [x] Validation command shape:
    - [x] Follow `skills/cache-expert/references/debugging.md` exactly.
    - [x] Use sub-agents for longer integration test runs/log parsing.

- Completion notes:
  - Converter now supports `MountType_SSH` via `withUnixSocket` + `withoutUnixSocket` cleanup.
  - dockerBuild hard-cutover path now passes SSH socket mappings into llbtodagger and supports Dockerfile `RUN --mount=type=ssh`.
  - Empty-key SSH mapping fallback allows one provided dockerBuild SSH socket to satisfy default or named SSH IDs (matching legacy translator behavior).
  - Remote syntax frontend behavior for known Dockerfile pragmas is now handled in Phase 20.

### Phase 20: Syntax Pragma Relaxation
- Goal:
  - Make dockerBuild cutover path laxer for Dockerfile syntax pragmas.
  - Allow known Dockerfile syntax pragma values and ignore them (continue conversion/build normally).
  - Fail fast only when pragma specifies an unknown/unsupported frontend reference.
- Scope:
  - In scope: `#syntax=...` handling in `core.Container.Build` preflight checks around Dockerfile bytes.
  - Out of scope: actually delegating to remote frontends or supporting non-Dockerfile frontend behavior.

- Design checklist:
  - [x] Replace current blanket hard-error on detected syntax pragma with allowlist behavior.
  - [x] Define "known Dockerfile syntax pragma" matcher.
    - [x] Accept canonical Dockerfile frontend references we explicitly recognize (Docker/Moby Dockerfile frontend refs).
    - [x] Treat pinned tag/digest variants of recognized Dockerfile frontends as allowed.
  - [x] On allowed syntax pragma, do not alter conversion flow:
    - [x] Continue using local `dockerfile2llb` + llbtodagger conversion path.
    - [x] Do not fetch or dispatch remote frontend.
  - [x] On unknown syntax pragma:
    - [x] Return explicit error that includes the pragma value and states it's unsupported in cutover mode.
  - [x] Keep behavior deterministic and obvious in logs/errors (no silent fallback beyond allowed-ignore path).

- Test checklist:
  - [x] Unit/functional coverage around syntax detection helper:
    - [x] known Dockerfile syntax pragma -> allowed
    - [x] unknown syntax pragma -> explicit error
  - [x] Integration coverage (`core/integration/dockerfile_test.go`):
    - [x] `with_syntax_pragma` passes on cutover path.
    - [x] `with_old_syntax_pragma` passes on cutover path.
    - [x] Add/keep a negative case for unknown syntax pragma that still errors clearly.
  - [x] Validation command shape:
    - [x] Follow `skills/cache-expert/references/debugging.md` command conventions.

- Completion notes:
  - Known Dockerfile syntax pragmas (for example `docker/dockerfile:*`) are now accepted and ignored by the hard-cutover path.
  - Unknown syntax pragmas now fail with explicit unsupported-frontend errors.
  - Existing syntax pragma integration coverage is passing again, including SSH remote-frontend subtests that use Dockerfile syntax pragma.

- Post-landing bookkeeping:
  - [x] Remove syntax-pragma item from current unsupported catalog.
  - [x] Update related historical notes in this whiteboard to reflect the new allowed-ignore policy.

### Phase 21: Public `query.http` Checksum Enforcement
- Goal:
  - Add checksum enforcement support to Dagger's public `query.http` API.
  - Allow callers to provide an expected digest and fail if downloaded content does not match.
- Scope:
  - In scope: `query.http` schema/API and HTTP download execution path in `core`.
  - Out of scope: llbtodagger `ADD --checksum` support (separate track).

- Design checklist:
  - [x] Add a new public optional string arg `checksum` to `query.http`.
    - [x] Add it in `core/schema/http.go` argument docs + `httpArgs`.
    - [x] Keep it public (not internal-only).
  - [x] Parse and validate checksum input.
    - [x] Parse with `digest.Parse(...)`.
    - [x] Return explicit error for invalid checksum format.
  - [x] Enforce checksum match in download path.
    - [x] Compare expected checksum vs actual downloaded content digest.
    - [x] Return explicit mismatch error containing expected + actual digest values.
    - [x] Ensure enforcement applies for both fresh download and cache-hit/304 paths.
  - [x] Include checksum in `query.http` resolver identity digest.
    - [x] Add checksum argument into DagQL digest mixin / cache-key hash composition in `core/schema/http.go` (`hashutil.HashStrings(...)` path).
  - [x] Regenerate Go SDK after schema API change.
    - [x] Run: `dagger -y call -m ./toolchains/go-sdk-dev generate`
    - [x] Worktree override required here: `dagger -y call -m ./toolchains/go-sdk-dev --workspace=. generate`

- Test checklist:
  - [x] Core unit/functional coverage:
    - [x] valid matching checksum succeeds.
    - [x] valid mismatched checksum fails with explicit mismatch error.
    - [x] invalid checksum string fails early with parse/validation error.
  - [x] Integration coverage (`core/integration/http_test.go`):
    - [x] add happy-path checksum test with deterministic content.
    - [x] add mismatch test.
    - [x] add invalid-checksum input test.
  - [x] Validation command shape:
    - [x] follow `skills/cache-expert/references/debugging.md` format for integration runs.

- Completion notes:
  - `query.http` now supports an optional public `checksum` argument and enforces digest match against downloaded content.
  - Resolver identity digest now mixes in the expected checksum value.
  - Go SDK regen required the whiteboard-documented worktree override (`--workspace=.`) to pick up local schema changes.
  - Focused integration coverage is passing with command shape from `skills/cache-expert/references/debugging.md`.

### Phase 22: Local Source `followPaths` Support (Internal-Only Path)
- Goal:
  - Support BuildKit `local.followpaths` emitted by Dockerfile->LLB conversion, without exposing this knob in public Dagger APIs.
  - Preserve current fail-fast posture for malformed attrs while adding faithful behavior for supported followPaths inputs.
- Design principles:
  - Keep filesync core behavior simple and unchanged for all existing call sites.
  - Add an explicit, narrow, internal-only path that is only used when IDs include followPaths (llbtodagger conversion path).
  - No fallback behavior; malformed/invalid followPaths still return explicit conversion/runtime errors.

- Implementation checklist:
  - [x] llbtodagger mapping:
    - [x] Parse `pb.AttrFollowPaths` JSON in local source conversion.
    - [x] Remove current hard-error on non-empty followPaths.
    - [x] Emit internal-only `followPaths` arg on `host.directory(...)` when present.
    - [x] Keep strict errors for invalid JSON / unknown attrs.
  - [x] Schema/internal API surface:
    - [x] Add internal-only `followPaths: [String!]` arg to `host.directory` args struct (`internal:"true"`).
    - [x] Keep it hidden from generated SDK/public callers.
  - [x] Host/filesync threading (explicit side path):
    - [x] Thread followPaths through `core.Host.Directory` into `filesync.SnapshotOpts`.
    - [x] Thread `SnapshotOpts.FollowPaths` through filesync `snapshot/sync` into:
      - [x] remote import opts (`engine.LocalImportOpts.FollowPaths`)
      - [x] local mirror filter construction (`fsutil.FilterOpt.FollowPaths`)
    - [x] Keep existing include/exclude/gitignore behavior unchanged when followPaths is empty.
  - [x] Cache identity parity:
    - [x] Ensure followPaths participates in identity where relevant (import metadata + dagql call args) so cache correctness matches semantics.
  - [x] Testing:
    - [x] Update llbtodagger unit tests:
      - [x] positive local source followPaths conversion case (ID contains internal arg)
      - [x] malformed followPaths JSON fails fast
    - [x] Add focused integration coverage for behavior:
      - [x] symlink target inclusion case that requires followPaths expansion
      - [x] command shape per `skills/cache-expert/references/debugging.md`
    - [x] Keep existing non-followPaths imports unchanged (regression check).

- Non-goals for this phase:
  - Do not expose followPaths in public SDK APIs.
  - Do not redesign filesync transport/protocol.
  - Do not broaden to unrelated local source attrs beyond current scope.

- Completion notes:
  - `local.followpaths` now converts to hidden `host.directory(followPaths: [...])` in llbtodagger IDs.
  - Host import path now threads followPaths explicitly through `SnapshotOpts` into remote and local filesync filters.
  - Filesync transport/protocol stayed unchanged; this is a narrow additive side path.
  - Focused coverage:
    - `go test ./util/llbtodagger -run 'TestDefinitionToIDLocal(Source|FollowPaths|FollowPathsInvalidUnsupported)$' -count=1`
    - `go test ./engine/filesync -count=1`
    - `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestLLBToDagger/TestLoadContainerFromConvertedIDLocalFollowPaths' --parallel=1`

- Expected touchpoints (planning index):
  - `util/llbtodagger/source.go`
  - `core/schema/host.go`
  - `core/host.go`
  - `engine/filesync/filesyncer.go`
  - `engine/filesync/remotefs.go`
  - `engine/filesync/localfs.go`
  - tests in `util/llbtodagger/*` and `core/integration/*`

### Phase 23: Hidden Archive Auto-Unpack Compatibility for File Copy
- Goal:
  - Support BuildKit `FileActionCopy.attemptUnpackDockerCompatibility` semantics (Dockerfile `ADD` local archive behavior) without exposing new public SDK surface.
  - Preserve current user-facing API while enabling llbtodagger fidelity for Dockerfile-generated LLB.
- Design principles:
  - Keep this as a narrow, explicit compatibility path behind internal-only args.
  - Match BuildKit behavior: attempt unpack first; if input is not an archive, fall back to normal copy.
  - No fallback to other unrelated behavior; malformed inputs still error.

- Implementation checklist:
  - [x] Schema/internal args:
    - [x] Add hidden `attemptUnpackDockerCompatibility bool` arg to `WithDirectoryArgs` (`internal:"true" default:"false"`).
    - [x] Add hidden `attemptUnpackDockerCompatibility bool` arg to `WithFileArgs` (`internal:"true" default:"false"`).
    - [x] Keep these args hidden from public SDK callers (internal-only pattern).
  - [x] Core directory/file copy implementation:
    - [x] Thread internal arg through `directorySchema.withDirectory` -> `core.Directory.WithDirectory`.
    - [x] Thread internal arg through `directorySchema.withFile` -> `core.Directory.WithFile`.
    - [x] Implement unpack-attempt helper in `core/directory.go` copy path:
      - [x] detect archive stream (gzip/bzip/xz/etc via decompressor + tar first header probe).
      - [x] if archive: untar into destination with existing owner/permissions handling semantics.
      - [x] if not archive: fall back to existing copy behavior.
    - [x] Preserve existing semantics for `doNotCreateDestPath` and `requiredSourcePath`.
  - [x] llbtodagger mapping:
    - [x] Remove current hard-error on `cp.AttemptUnpackDockerCompatibility`.
    - [x] Emit hidden `attemptUnpackDockerCompatibility: true` on generated `withDirectory`/`withFile` call IDs when LLB copy action sets it.
    - [x] Keep strict errors for unrelated unsupported copy attrs.
  - [x] Behavior and safety notes:
    - [x] Ensure unpack path cannot escape destination root (path traversal hardening).
    - [x] Ensure archive extraction path follows same ownership/chmod intent used by copy path where applicable.
    - [x] Keep the feature Linux-first if platform nuances require; fail clearly where unsupported.
  - [x] Tests:
    - [x] Unit tests in `util/llbtodagger/convert_test.go`:
      - [x] verify `AttemptUnpackDockerCompatibility` maps to hidden arg (instead of unsupported error).
      - [x] keep non-attempt copy behavior unchanged.
    - [x] Unit/integration tests in core:
      - [x] positive: local tar archive is unpacked when hidden arg true.
      - [x] fallback: non-archive file is copied as-is when hidden arg true.
      - [x] regression: hidden arg false keeps current copy semantics.
    - [x] Integration coverage in `core/integration/llbtodagger_test.go`:
      - [x] Dockerfile case using local `ADD` archive produces expected extracted tree via converted ID.
      - [x] command shape per `skills/cache-expert/references/debugging.md`.

- Non-goals for this phase:
  - Do not expose a public API flag for unpack behavior.
  - Do not rework all copy semantics; only compatibility path for `AttemptUnpackDockerCompatibility`.
  - Do not add Dockerfile parsing logic in llbtodagger (LLB-driven only).

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
- COPY semantics nuance (resolved):
  - `TestDockerfile/TestDockerBuild/onbuild_command_is_published` failed because file-op COPY lowered to `withDirectory(include: [...])` silently no-oped when source was missing.
  - Added internal `requiredSourcePath` propagation to enforce BuildKit-like missing-source error for literal single-path COPY cases.

## Current Explicit Unsupported Cases (Implemented)
- All `BuildOp` vertices.
- All `blob://` sources.
- All `oci-layout://` sources (currently unsupported).
- `ExecOp` with non-default network/security, non-default mount content cache, or unsupported metadata fields.
- `FileOp` copy actions with `alwaysReplaceExistingDestPaths`.
- `FileOp` mkdir without `makeParents=true`.
- `FileOp` mkfile with non-UTF8 content.
- Named ownership on `mkdir` file actions (Dockerfile `WORKDIR` path when `USER` is named).
- Named ownership on `mkfile` file actions.
- Named ownership on copy actions when no container context is available.

## Detailed Unsupported Nuance Catalog (Exhaustive, Dockerfile-Classified)

### Unsupported and relevant to Dockerfile-generated LLB

#### HIGH

#### MEDIUM

- Named ownership for `mkdir` actions is unsupported (Dockerfile `WORKDIR` path when `USER` is named).
  - BuildKit encodes named ownership with an input index (`UserOpt_ByName.Input`) and resolves names against that input's filesystem.
  - Resolution reads `/etc/passwd` and `/etc/group` from the mounted input used for the action (the stage rootfs context for `WORKDIR`-driven `mkdir`).
  - Supporting this faithfully in llbtodagger `mkdir` mapping requires container-context name resolution at that action point (or equivalent API behavior), not plain directory-only `chown`.

- Named ownership for copy actions without container context is unsupported.

- Non-default network mode is unsupported (`RUN --network=...`).

- Non-sandbox security mode is unsupported (`RUN --security=...` when enabled).

#### LOW

- Platform `OSVersion` and `OSFeatures` are unsupported.

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
- Named ownership for `mkfile` actions is unsupported.
- `mkfile` timestamp override is unsupported.
- `mkfile` non-UTF8/binary payload is unsupported.
- `copy` `alwaysReplaceExistingDestPaths=true` is unsupported.
- Unknown `UserOpt` discriminator in `chown` is unsupported.
- Invalid env entries (without `name=value`) are rejected.
- Exposed ports with invalid format are rejected.
- Exposed ports outside `1..65535` are rejected.
- Exposed ports using protocols other than TCP/UDP are unsupported.

## Crucial Notes To Not Forget
- `skills/cache-expert/references/debugging.md` is the authoritative source for how to run integration tests; follow it exactly.
  - This applies to both primary agent commands and any subagent that runs/tests/parses integration output.
- In `core/schema`, APIs prefixed with `_` are internal-only:
  - they can be called via raw ID construction,
  - they are intentionally not codegen'd into SDK clients.
  - Also: args/fields tagged with ``internal:"true"`` are hidden from callers/codegen even without an `_` prefix on the field/function name.
- After any engine schema API change, regenerate the Go SDK used by integration tests.
  - Required command: `dagger -y call -m ./toolchains/go-sdk-dev generate`
  - Worktree gotcha: if module resolution fails, pass `--workspace=.` explicitly:
    - `dagger -y call -m ./toolchains/go-sdk-dev --workspace=. generate`
- No custom-op handling in this package.
  - Ignore `dagger.customOp`.
  - Do not decode/convert `dagop.fs`, `dagop.raw`, `dagop.ctr`.
- Unsupported/unfaithful mapping policy is strict fail-fast for now.
  - First unsupported/imperfect mapping returns error immediately.

## Whiteboard Usage Rules (For This Task)
- Every time scope, assumptions, or mapping behavior changes, update this file in the same change.
- Keep section boundaries strict:
  - `Unsupported and relevant to Dockerfile-generated LLB` must contain only Dockerfile-relevant items.
  - Non-Dockerfile/non-canonical items must go in `Unsupported but outside Dockerfile instruction support (or malformed/non-canonical LLB)`.
- For each implemented op mapper, update:
  - checklist box,
  - coverage matrix status,
  - mismatch log (if new gaps found).
- Keep this file current across context compaction; treat it as the source-of-truth progress ledger.
