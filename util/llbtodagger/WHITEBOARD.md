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
- `ExecOp` with non-default network/security, secret env, secret/ssh mounts, non-default mount content cache, or unsupported metadata fields.
- `FileOp` copy actions with archive auto-unpack, `alwaysReplaceExistingDestPaths`, or `createDestPath=false`.
- `FileOp` mkdir without `makeParents=true`.
- `FileOp` mkfile with non-UTF8 content.
- Named ownership on `mkdir` file actions (Dockerfile `WORKDIR` path when `USER` is named).
- Named ownership on `mkfile` file actions.
- Named ownership on copy actions when no container context is available.

## Detailed Unsupported Nuance Catalog (Exhaustive, Dockerfile-Classified)

### Unsupported and relevant to Dockerfile-generated LLB

#### HIGH

- Secret environment variables are unsupported (`RUN --mount=type=secret,env=...`).
- Secret mounts are unsupported (`RUN --mount=type=secret`).

- SSH mounts are unsupported (`RUN --mount=type=ssh`).

- Proxy environment injection is unsupported (proxy build-arg path).

- HTTP checksum enforcement attr is unsupported (`ADD --checksum` path).

- Local source `followPaths` is unsupported.
  - This commonly appears for Dockerfile context copies that reference specific paths (for example `COPY package.json /app/package.json`), where the frontend narrows context transfer to only used paths.
  - It is not always present: broad context usage like `COPY . /app` typically marks `/` and does not emit a narrowed `followPaths` list.
  - BuildKit uses `followPaths` to resolve symlinks in selected paths so link targets are also included in context transfer.
  - Current llbtodagger behavior is strict fail-fast when `local.followpaths` is present.

#### MEDIUM

- Empty named user in `chown` is unsupported when it is not the Dockerfile group-only representation (`--chown=:...`).
  - BuildKit/Dockerfile group-only form (`--chown=:gid`) can surface as empty named user + explicit group; we normalize this to `0:<gid>`.
  - Other empty-name combinations are treated as malformed/ambiguous and remain fail-fast.
  - Follow-up nuance: for user-only named ownership (`--chown=user`), BuildKit semantics use the user's primary GID from passwd; ensure container-side default-group behavior stays aligned before broadening empty-name acceptance.

- `copy` archive auto-unpack compatibility mode is unsupported (`ADD` local archive path).
  - BuildKit represents this with `FileActionCopy.attemptUnpackDockerCompatibility`.
  - Dockerfile frontend sets it from `ADD` semantics (`ADD` defaults to unpack for local archives; `COPY` does not auto-unpack; `ADD --unpack` can override behavior).
  - Upstream executor path: for each matched source, try archive-detection + untar into destination; if not an archive, fall back to normal copy.
  - Current llbtodagger mapping errors on this flag because we do not yet model that conditional unpack-or-copy behavior in the Dagger ID translation.

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
- `copy` with `createDestPath=false` is unsupported.
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
