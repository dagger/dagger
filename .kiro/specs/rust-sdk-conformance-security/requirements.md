# Requirements Document: Rust SDK Conformance, Platform, and Security Gates

## Introduction

Feature 8 turns the engine-free implementation closures from Features 2–7 into an
admissible Go-level Rust SDK claim. It accounts for every integration capability in
the completeness ledger, closes the native platform matrix, verifies the security and
supply-chain boundary, and performs one bounded exact-target SDK sign-off. It does not
reimplement completed features merely to test them, run the complete Go SDK suite, or
build unrelated SDKs.

The engine schema and target engine source define public wire and workspace behaviour.
The pinned `sdk-sdk` checks define common SDK lifecycle behaviour only within their
explicit check scope. The Definitive_Go_SDK and the selected Dagger integration tests
define observable behaviour outside that harness scope. Rust policy continues to own
public API shape, ownership, process and filesystem safety, error structure, and
idiomatic equivalence. A passing test from another SDK is authority evidence, not
passing Rust evidence.

The Exact_Target is Dagger commit
`25300124ca110612edc09c43f89cb5fad6028170`, engine and Rust SDK version
`v1.0.0-beta.10`, Definitive_Go_SDK commit
`1309520660f6a5b35ef97b4fbe151e32a06a8dc5`, `sdk-sdk` commit
`8c164424b7a8a37b33a77367ef7547490d5b87b5`, Rust 1.97.1, and edition 2024.
The Subject_Revision is the fork revision whose Rust implementation and exact engine
overlay are being signed off; it remains distinct from the pinned target revision.

Feature 8 currently owns 1,081 ledger capabilities: 1,072 `Missing` observations from
the target Dagger integration suite and nine `Partial` behaviours from the definitive
Go client tests. This is a deliberately broad discovery inventory, not an instruction
to port Go test syntax or execute every other SDK. Every item must receive an exact
applicability decision and evidence route before the count can move.

Ordinary Feature 8 development remains engine-free and Rust-first. The one approved
infrastructure exception is a short Signoff_Host_Preflight performed before sign-off
implementation is trusted: it may start a pinned prebuilt engine smoke solely to prove
that the selected host can run Dagger. It must not build the Exact_Target, execute an
SDK case, or become conformance evidence. Final SDK_Signoff later consumes matching
engine-free closure evidence, imports or builds one artifact, starts one exact-target
engine, installs one Rust baseline, fans out isolated Rust cases, and emits one atomic
verdict.

The first host used to validate this contract is a dedicated Namespace XL Linux/amd64
devbox. Namespace is an execution choice, not a behavioural authority, repository
dependency, or permanent sign-off requirement. Any provider-neutral host satisfying
the same preflight contract may reproduce the run. Feature 9 consumes the successful
Feature 8 verdict for publication and stable release presentation.

## Glossary

- **Applicable_Capability:** A ledger capability whose observable contract applies to
  a Rust client, generated client, Rust-authored module, packaged runtime, or SDK
  integration on the Exact_Target.
- **Applicability_Record:** The canonical, reviewed mapping from one Feature 8
  Capability_ID to its authority anchor, Rust applicability disposition, assertion,
  case route, and terminal-status policy.
- **Artifact_Component:** Exactly one of the engine, CLI, mandatory engine-packaged Go
  runtime content, or Rust SDK content bound into the Exact_Target_Signoff_Artifact.
- **Artifact_Security_Report:** The target-bound result of provenance, component,
  vulnerability, and secret checks over the exact artifact bytes used by SDK_Signoff.
- **Atomic_Signoff_Verdict:** One canonical pass or fail record covering the complete
  Case_Catalog, platform and security inputs, build/start counters, timings, and
  evidence identities; no successful subset is separately admissible.
- **Case_Attempt:** One recorded execution of a Case_Definition against the active
  engine and a workspace branched from the Installed_Rust_Baseline.
- **Case_Catalog:** The canonical, digest-bound, closed inventory of all exact-engine
  Case_Definitions required for one target and platform before an engine starts.
- **Case_Definition:** One isolated Rust-owned exact-engine scenario with immutable
  inputs, assertions, Capability_ID scope, resource bounds, and retry policy.
- **Common_Harness_Case:** One of the 17 subject-conformance checks defined by the
  pinned `sdk-sdk`; the harness's own `init-module-renders-root-type` self-check is not
  a Common_Harness_Case for the Rust subject.
- **Complete_Status:** `Implemented`, `Idiomatic_Equivalent`, or a justified
  `Inapplicable` classification under the Feature 1 transition policy.
- **Conformance_Assertion:** One stable, independently inspectable observable claim
  derived from an authority source and exercised through Rust production behaviour.
- **Definitive_Go_SDK:** `github.com/dagger/dagger-go-sdk` at commit
  `1309520660f6a5b35ef97b4fbe151e32a06a8dc5`.
- **Engine_Owned_Capability:** A selected integration observation whose behaviour is
  entirely implemented by the target engine or CLI and imposes no Rust SDK mechanism
  or Rust-observable compatibility obligation.
- **Exact_Target:** The complete descriptor selected by
  `sdk/rust/completeness/target.json`.
- **Exact_Target_Signoff_Artifact:** An immutable, exportable and importable artifact
  for one Exact_Target, Subject_Revision, platform, component-input set, and toolchain
  set, containing or content-addressing the actual bytes required by SDK_Signoff.
- **Foreign_SDK_Mechanism:** A language-specific parser, template, runtime, package
  manager, or public API behaviour belonging to an SDK other than Rust.
- **Implementation_Closure_Bundle:** The canonical set of matching engine-free
  Feature 2–7 closure records, native-platform results, Rust hygiene results, and
  security results admitted before SDK_Signoff starts an engine.
- **Infrastructure_Attempt:** A host, artifact, or service operation that does not
  execute a Case_Definition and cannot prove a Capability_ID.
- **Installed_Rust_Baseline:** One immutable runner and workspace state produced by a
  canonical exact-artifact CLI/Rust SDK installation, from which all isolated case
  workspaces branch.
- **Native_OS_Job:** An engine-free test job running process, filesystem, archive,
  cache, permissions, redaction, and cleanup behaviour on Linux, macOS, or Windows.
- **Portable_Platform_Matrix:** Native_OS_Jobs for all three operating-system families
  plus exhaustive descriptor coverage for Linux, macOS, and Windows crossed with
  amd64 and arm64.
- **Production_Distribution_Observation:** The live result of the stable default
  connector's production CLI selection. For the current unavailable beta.10 checksum
  manifest, the definitive 403/404 compatibility path may select the exact built CLI
  from `PATH`; that observation does not claim a successful verified download.
- **Secret_Canary_Set:** High-entropy non-production values injected into every
  credential-bearing sign-off boundary and forbidden from all persisted or rendered
  outputs.
- **Signoff_Host:** A provider-neutral Linux/amd64 execution environment satisfying the
  Signoff_Host_Profile used for this Feature 8 exact-engine verdict.
- **Signoff_Host_Preflight:** A bounded infrastructure-only probe of the selected host's
  platform, storage, container runtime, privileged engine capability, service network,
  persistence, and export/import boundary.
- **Signoff_Host_Profile:** The canonical required platform, resource, storage,
  container, network, persistence, and time-budget contract for one sign-off run.
- **Signoff_Run_Plan:** The immutable target, host-profile, artifact, case-catalog,
  concurrency, timeout, retry, network, and output policy accepted before SDK_Signoff.
- **Subject_Revision:** The immutable fork revision or equivalent source digest
  containing the Rust implementation under evaluation.
- **SDK_Signoff:** The bounded exact-engine evaluation that consumes one
  Implementation_Closure_Bundle and produces one Atomic_Signoff_Verdict.

## Target State

The Feature 8 scope is exact and reviewable. Every one of the 1,081 current ledger
items has an Applicability_Record. A Rust-observable behaviour maps to one or more
Rust-owned Conformance_Assertions and Case_Definitions. A Foreign_SDK_Mechanism or
Engine_Owned_Capability may become `Inapplicable` only through capability-local
decision evidence proving that no Rust obligation is being discarded. Multiple IDs
may share one assertion when they express the same observable invariant, but no ID is
closed by a file-wide wildcard, another SDK's pass, or an unmapped aggregate result.

Feature 8 implementation checkpoints exercise the applicability engine, artifact
state machine, case planner, platform policy, security policy, counters, evidence
admission, and verdict renderer directly in Rust. Checked generated assets and prior
closure records are reused unless their owning identities change. Checkpoints do not
construct a Dagger engine, run an SDK module, build another SDK, perform repository-wide
generation, or replay exact-engine cases.

Before the exact-target graph is built, Signoff_Host_Preflight verifies that the
selected host can support the run. The initial Namespace XL profile provides Linux
amd64, 32 vCPUs, 64 GiB memory, a 200 GB persistent workspace volume, and a Docker
daemon. Those observed values are informative; the canonical decision is whether the
host satisfies the reviewed Signoff_Host_Profile. The preflight uses a pinned prebuilt
engine smoke and cannot become a substitute for the later exact-target engine.

SDK_Signoff accepts only a closed Signoff_Run_Plan and matching closure bundle. It
builds or imports exactly one Exact_Target_Signoff_Artifact. A built artifact compiles
each Artifact_Component at most once. An imported artifact supplies and verifies the
same content-addressed bytes without recompilation. The graph excludes unrelated SDK
builders, tests, generators, examples, and distribution-wide build paths.

The runner starts one exact-target engine, creates one Installed_Rust_Baseline, and
branches an isolated workspace for every case. All cases use the same engine service
and artifact. Bounded concurrency may reduce elapsed time, but a case cannot observe
another case's files, environment, session credentials, cache namespace, result, or
failure. A case assertion failure remains a failure even if a later attempt passes. An
infrastructure interruption may be retried only within the declared policy and cannot
start a second engine in the same run.

The Case_Catalog includes the applicable pinned `sdk-sdk` subject checks, Feature 3's
stable-default-connector observation, Feature 4's representative Core shape paths,
Feature 5's exact integration matrix, Feature 6's complete module and packaged
self-consumer matrix, Feature 7's five deferred standalone-client cases, the nine
definitive Go client behaviours, and every additional Rust-observable assertion
derived from the 1,072 selected integration items. The complete Go SDK suite and other
language SDK suites never run inside Rust sign-off.

The Portable_Platform_Matrix proves production OS-specific behaviour on Linux, macOS,
and Windows without an engine, while pure descriptor tests cover amd64 and arm64 for
each OS. The first exact-engine verdict is Linux/amd64. Any later exact-engine platform
claim requires a separate artifact and verdict; evidence from one platform is never
silently widened to another.

Security remains fail-closed. All Rust roots resolve committed lockfiles and pass
Cargo Deny. Unsafe code remains denied. Every external image, tool, archive, and
scanner is immutable and provenance-reviewed. The exact engine artifact is scanned
without rebuilding it, and scanner plus vulnerability-database identities are recorded.
Any exception is explicit, owned, scoped, justified, and automatically removable when
its expiry condition becomes true. Secret canaries must be absent from source outputs,
artifacts, files, cache keys, diagnostics, traces, reports, and verdicts.

The Atomic_Signoff_Verdict binds every identity, case outcome, security result,
platform result, build/start count, and phase timing. A missing, skipped, unknown,
stale, failed, leaking, duplicated, or overbroad input produces one failed verdict and
no partial sign-off evidence. Only an admitted passing verdict may close Feature 8
blockers and unblock Feature 9.

## Evidence From Current Code

Repository citations to target behaviour use Target_Revision unless another revision
is stated. Current Rust citations describe `main` after Feature 7 merge commit
`90ba78d0a4fc3fb66d5dbe113c2143b13419c8b7`.

- **Exact target:** `sdk/rust/completeness/target.json` pins the target engine, Go SDK,
  sdk-sdk, schema, CLI, Rust SDK, edition, and Rust toolchain identities. The current
  target digest recorded by Feature 7 closure is
  `sha256:cca4bdcf5f934b5b1acfc03a8bb6db081856f3c1110ebb28f7bdca592efc0f4f`.
- **Current Feature 8 ledger scope:**
  `sdk/rust/completeness/artifacts/ledger.json` contains 1,081 Feature 8-owned rows:
  1,072 `Missing` `go-integration-tests` rows and nine `Partial` `go-client` rows. The
  lexicographically sorted compact-JSON Capability_ID list has digest
  `sha256:2969bd8fde19fc17d327cef637b9d848eca01040e88caffc09a4e9a4ad9bc5f9`.
- **Integration authority selection:** `sdk/rust/completeness/authorities.json` selects
  the pinned module, workspace, SDK CLI, and future-test sources from the target
  Dagger repository and preserves explicit exclusions as audit history. The source
  digest is `sha256:bc5dfb40a9c0247523b2c3f34d5aeba3c413254552608d9ae81c381d5737118b`.
- **Definitive client behaviours:** `sdk/go/client_test.go:33-302` at the Definitive
  Go SDK revision defines the selected directory, Git, container, list, and typed
  execution-error observations. Those nine rows have Rust implementation evidence but
  still lack Feature 8 integration evidence.
- **Pinned common harness:**
  `sdk/rust/completeness/sources/sdk-sdk/8c164424b7a8a37b33a77367ef7547490d5b87b5/sdk-sdk.dang:91-284`
  defines 17 subject checks for SDK installation, module initialization, generation,
  loading, options, dependencies, cwd scoping, file ownership, and generator exposure.
  Lines 25 and 286-301 explicitly exclude client generation from that scope and define
  one harness-self check respectively.
- **Current harness executor:** `toolchains/rust-sdk-dev/completeness.go:72-147` runs
  the beta.9 baseline profile in a separate beta.9 engine. It is useful baseline
  evidence but is not the Feature 8 exact-target, single-engine sign-off.
- **Focused source graph:** `toolchains/rust-sdk-dev/main.go:169-201` excludes unrelated
  SDK source and build output while retaining the engine, CLI, required Go source,
  Rust content, and adapter inputs.
- **Reusable Rust content:** `toolchains/rust-sdk-dev/main.go:397-469` creates one
  digest-identified Rust content object with manifest, descriptor, dependency,
  mapping, and completeness identities. It retains a live graph object but does not
  yet define an export/import artifact across host restarts.
- **One-service foundation:** `toolchains/rust-sdk-dev/main.go:568-672` validates a
  closed ten-case Feature 5 selector, starts one focused service, installs one common
  runner for most cases, and uses bounded concurrency. The `resolution` branch still
  creates a separate installation path, and the evidence lacks umbrella-wide
  build/start counters and phase timings.
- **Current engine evidence:** `toolchains/rust-sdk-dev/main.go:675-730` requires all
  ten Feature 5 case names and binds target, toolchain, dependency, operation,
  manifest, descriptor, mapping, and completeness identities. It does not consume all
  child-feature closures, platform results, security results, or the Feature 8 case
  catalog.
- **Feature 3 live handoff:**
  `.kiro/specs/rust-sdk-transport-observability/tasks.md:487-529` leaves exact-target
  stable-default-connector evidence open because the beta.10 production checksum
  manifest returns HTTP 403 and the installed PATH CLI was outside target. The
  definitive fallback permits an exact CLI from PATH after that 403/404; Feature 8 can
  install the artifact's exact CLI on PATH and must record that it observed fallback,
  not download.
- **Feature 5 sign-off handoff:**
  `.kiro/specs/rust-sdk-engine-integration/requirements.md:929-998` defines the exact
  installation, generation, runtime, protocol, negative, evidence, and closure
  observations consumed by Feature 8.
- **Feature 6 sign-off handoff:**
  `.kiro/specs/rust-sdk-module-authoring/requirements.md:1031-1074` requires complete
  TypeDef registration, a packaged self-consumer, representative module semantics,
  applicable sdk-sdk checks, and exact evidence binding.
- **Feature 7 closure handoff:**
  `sdk/rust/completeness/evidence/client-generation-closure.json:1-45` binds the
  admitted engine-free closure and lists five exact-engine cases: initialized local
  client, pinned remote client, schema regeneration, Core query, and namespaced module
  query.
- **Current Rust security CI:** `.github/workflows/rust-sdk-security.yml:1-102` uses a
  read-only token, a 15-minute Ubuntu job, Cargo Deny, focused source/security tests,
  direct Go runtime metadata tests, and public package checks. It is not a native
  Linux/macOS/Windows matrix and does not scan the final exact-target engine artifact.
- **Dependency and unsafe policy:** `sdk/rust/deny.toml:1-45` rejects active
  advisories, unapproved licenses, wildcards, unknown registries, and unknown Git
  sources. `sdk/rust/Cargo.toml:79-84` denies unsafe code and undocumented unsafe
  blocks across the workspace.
- **Existing artifact scanner:** `toolchains/security/main.dang:17-67` pins Trivy
  0.69.3 by image digest and can scan a supplied engine tarball for high and critical
  OS/library vulnerabilities. Its current output does not bind scanner provenance,
  vulnerability-database identity, target artifact digest, or reviewed exceptions.
- **Dependency automation:** `.github/dependabot.yml` includes the root Rust workspace
  and three example Cargo roots. It also contains an npm entry for `/sdk/rust`; Feature
  8 must make the update surface match the actual Rust package ecosystems rather than
  treating an inapplicable updater as coverage.
- **Initial Namespace host observation:** `devbox list --output json` and a read-only
  remote probe on 2026-08-12 reported a private Linux/amd64 Namespace XL devbox with 32
  vCPUs, 64 GiB memory, a 200 GB whole-system-persistent volume, Docker 29.3 using
  overlayfs, approximately 198 GB free workspace storage, and Rust 1.97.1. Host Go was
  1.25.3, proving that sign-off must use the artifact's pinned Go 1.26.1 build image
  rather than ambient host Go.

## Completeness Contract Policy

### Existing Feature 8 Scope

| Authority | Current status | Count | Feature 8 policy |
|---|---:|---:|---|
| Definitive Go client tests | `Partial` | 9 | Execute equivalent Rust directory, Git, container, list, and typed execution-error assertions |
| Target Dagger integration tests | `Missing` | 1,072 | Review every item for Rust applicability; map applicable observations to Rust cases and justify genuine engine-only or foreign-SDK inapplicability |
| **Existing total** | **Blocking** | **1,081** | Exact scope digest `sha256:2969bd8fde19fc17d327cef637b9d848eca01040e88caffc09a4e9a4ad9bc5f9` |

The 1,072 integration items contain 61 dynamic subtests, one Go function, four Go
methods, 663 subtests, 273 tests, 63 test-table rows, and 16 Go types. The extractor
inventory is intentionally syntax-complete; a Go helper type or foreign runtime test
does not automatically become a Rust runtime obligation.

### Selected Integration Source Files

| Authority source | Rows | Review emphasis |
|---|---:|---|
| `core/integration/module_call_test.go` | 165 | Call, argument, return, error, and state semantics |
| `core/integration/module_typescript_test.go` | 105 | Shared invariants versus TypeScript-only mechanisms |
| `core/integration/module_path_inputs_test.go` | 91 | Directory, file, ignore, default-path, and Git-context semantics |
| `core/integration/module_python_test.go` | 76 | Shared invariants versus Python-only mechanisms |
| `core/integration/module_go_test.go` | 58 | Shared invariants versus Go-only mechanisms |
| `core/integration/module_dang_test.go` | 49 | Shared invariants versus Dang-only mechanisms |
| `core/integration/module_runtime_behavior_test.go` | 48 | Runtime lifecycle and isolation |
| `core/integration/module_type_test.go` | 47 | Public TypeDef and invocation semantics |
| `core/integration/module_loading_test.go` | 39 | Loading and source resolution |
| `core/integration/module_config_test.go` | 36 | Module configuration semantics |
| `core/integration/module_java_test.go` | 35 | Shared invariants versus Java-only mechanisms |
| `core/integration/module_php_test.go` | 32 | Shared invariants versus PHP-only mechanisms |
| `core/integration/module_elixir_test.go` | 29 | Shared invariants versus Elixir-only mechanisms |
| `core/integration/workspace_modules_test.go` | 25 | Workspace/module isolation and selection |
| `core/integration/module_definition_test.go` | 24 | Module definition and metadata |
| `core/integration/module_introspection_cli_test.go` | 16 | Introspection CLI observations |
| `core/integration/module_self_calls_test.go` | 16 | Self-call semantics |
| `internal/cmd/dagger/module_init_test.go` | 16 | CLI initialization behaviour |
| `core/integration/module_custom_sdk_test.go` | 15 | Custom SDK lifecycle shared with Rust |
| `core/integration/module_validation_test.go` | 15 | Validation and diagnostics |
| `internal/cmd/dagger/sdk_init_dynamic_test.go` | 15 | Dynamic SDK initialization |
| `core/integration/module_dependency_runtime_test.go` | 14 | Dependency runtime behaviour |
| `core/integration/module_constructor_test.go` | 12 | Constructor behaviour |
| `core/integration/module_iface_test.go` | 11 | Interface behaviour |
| Definitive `client_test.go` | 9 | Public client directory, Git, container, list, and execution-error behaviour |
| `core/integration/module_current_module_test.go` | 9 | Current-module context |
| `core/integration/module_private_deps_test.go` | 9 | Private dependency visibility |
| `core/integration/module_runtime_codegen_test.go` | 9 | Runtime code-generation boundary |
| `core/integration/module_terminal_test.go` | 9 | Terminal behaviour observable through Rust |
| `core/integration/module_error_test.go` | 7 | Engine and application error behaviour |
| `core/integration/module_deprecation_test.go` | 6 | Deprecation metadata and diagnostics |
| `core/integration/module_engine_version_test.go` | 6 | Required-engine version behaviour |
| `core/integration/module_benchmark_test.go` | 5 | Correctness assertions only; benchmark measurements are not parity gates |
| `internal/cmd/dagger/module_sdk_test.go` | 4 | CLI SDK selection behaviour |
| `core/integration/module_builtin_dang_test.go` | 3 | Built-in Dang dependency versus Rust applicability |
| `internal/cmd/dagger/module_test.go` | 3 | General module CLI behaviour |
| `core/integration/module_cli_suite_test.go` | 2 | CLI suite lifecycle |
| `core/integration/module_config_compat_test.go` | 2 | Configuration compatibility |
| `core/integration/module_config_suite_test.go` | 2 | Configuration suite lifecycle |
| `core/integration/module_suite_test.go` | 2 | Integration-suite scaffolding versus observable behaviour |
| `core/integration/module_tui_test.go` | 2 | TUI presentation versus SDK-observable behaviour |
| `core/integration/module_up_test.go` | 2 | Module-up lifecycle |
| `core/integration/module_dependency_cli_test.go` | 1 | Dependency CLI behaviour |

This table accounts for all 1,081 current rows. The canonical machine artifact remains
the complete ordered Capability_ID list and its digest; the design must not depend on
these prose groups for admission.

### New Rust Policy Capabilities

Feature 8 adds the following Rust-specific capabilities because the selected Go and
harness declarations do not inventory them:

```text
policy/rust-policy/conformance-capability-scope
policy/rust-policy/conformance-applicability-accounting
policy/rust-policy/conformance-case-catalog
policy/rust-policy/conformance-engine-free-checkpoint
policy/rust-policy/signoff-host-preflight
policy/rust-policy/signoff-exact-target-artifact
policy/rust-policy/signoff-artifact-import-reuse
policy/rust-policy/signoff-closure-evidence
policy/rust-policy/signoff-single-engine
policy/rust-policy/signoff-single-rust-baseline
policy/rust-policy/signoff-isolated-case-fanout
policy/rust-policy/signoff-case-retry-honesty
policy/rust-policy/signoff-atomic-verdict
policy/rust-policy/signoff-duplicate-work-rejection
policy/rust-policy/signoff-phase-budget
policy/rust-policy/platform-native-matrix
policy/rust-policy/security-locked-supply-chain
policy/rust-policy/security-artifact-provenance
policy/rust-policy/security-artifact-vulnerability-scan
policy/rust-policy/security-secret-canary
policy/rust-policy/security-expiring-exception
```

### Applicability Dispositions

| Disposition | Terminal policy | Required evidence | Forbidden shortcut |
|---|---|---|---|
| Rust-observable, same mechanism | `Implemented` | Production Rust case plus exact assertion and target evidence | Passing Go test alone |
| Rust-observable, idiomatic mechanism | `Idiomatic_Equivalent` | Production Rust case plus reviewed equivalence decision | Symbol or ownership parity with Go |
| Engine-owned with no Rust obligation | `Inapplicable` | Capability-local decision proving no Rust input, output, lifecycle, or compatibility effect | File-wide engine-only classification |
| Foreign SDK mechanism with no Rust obligation | `Inapplicable` | Capability-local decision naming the foreign mechanism and routing any shared invariant to a Rust assertion | Treating a language-named source file as wholly irrelevant |
| Applicable but unverified | `Missing` or `Partial` | Exact residual gap remains in the ledger | Reclassifying to make the report green |

### Applicability Record Contract

| Field | Target policy | Error if invalid | Side-effect impact |
|---|---|---|---|
| `capability_id` | One exact active Feature 8 ID | Unknown, duplicate, or out-of-scope ID | No record admitted |
| `authority_anchor` | Exact source path, locator, repository, and revision | Missing or drifted anchor | No status change |
| `source_fingerprint` | Current ledger fingerprint | Mismatch | Candidate rejected as stale |
| `disposition` | One reviewed disposition from the table above | Unknown or unjustified value | Candidate rejected |
| `assertion_ids` | Non-empty for every applicable item | Empty or unknown assertion | Capability remains blocking |
| `case_ids` | Exact Case_Catalog routes for live assertions | Unknown or overbroad case | Sign-off catalog rejected |
| `decision_evidence` | Required for equivalence or inapplicability | Missing, generic, or unrelated decision | Non-Implemented transition rejected |
| `terminal_policy` | Feature 1 status allowed by disposition | Status/disposition mismatch | Ledger transition rejected |

## Sign-off Host Preflight Policy

| Concern | Required observation | Failure result | Evidence boundary |
|---|---|---|---|
| Host profile | OS, architecture, CPU, memory, workspace capacity, and profile version satisfy Signoff_Run_Plan | Preflight fail before artifact work | Infrastructure only |
| Persistent workspace | Artifact and cache roots survive process/session restart under the selected host contract | Preflight fail | No Capability_ID |
| Container daemon | Versioned API responds and reports the expected storage driver and capacity | Preflight fail | No SDK case |
| Privileged engine capability | Pinned prebuilt smoke engine reaches ready state and stops cleanly | Preflight fail | Never exact-target evidence |
| Service networking | Isolated client reaches the smoke engine only through the declared service address | Preflight fail | No public query claim |
| Export/import | A canary content object exports, verifies, imports, and preserves its digest | Preflight fail | No target artifact claim |
| Cache reuse | A second identical canary build observes reuse without changing the output digest | Preflight fail | No target build counter |
| Secret isolation | Canary environment values do not enter retained host output | Preflight fail | No real credential used |
| Time and storage budget | Every preflight phase remains within the declared bounds | Preflight fail with phase identity | No silent continuation |

The smoke engine is outside SDK_Signoff and therefore outside the exact-one-engine
counter. It exists only because Docker metadata cannot prove Dagger's privileged
runtime behaviour. It must use a reviewed prebuilt digest, must not build the target,
and must terminate before the sign-off run begins.

## Exact-Target Artifact Policy

### Artifact Manifest Contract

| Field | Target policy | Error if invalid | Side-effect impact |
|---|---|---|---|
| `format_version` | One supported manifest format | Unknown version | Import/build rejected |
| `target_descriptor_digest` | Digest of the complete Exact_Target | Mismatch | Artifact rejected |
| `target_revision` | Exact target commit | Missing or different revision | Artifact rejected |
| `subject_revision` | Full reachable fork revision or explicit source digest | Mutable, missing, or unreachable identity | Artifact rejected |
| `platform` | Exact OS/architecture of artifact execution | Host mismatch | Sign-off not started |
| `engine_input_digest` | Complete engine build input identity | Missing or stale | Artifact rejected |
| `cli_input_digest` | Complete CLI build input identity | Missing or stale | Artifact rejected |
| `go_runtime_digest` | Mandatory engine-packaged Go runtime content identity | Missing or overbroad content | Artifact rejected |
| `rust_manifest_digest` | Rust SDK packaged-content manifest identity | Missing or malformed digest | Artifact rejected |
| `rust_descriptor_digest` | Rust SDK dependency/runtime descriptor identity | Missing or malformed digest | Artifact rejected |
| `toolchain_digests` | Rust, Go, base-image, builder, and scanner identities | Tag-only or unreviewed identity | Artifact rejected |
| `component_digests` | Canonical digest for every Artifact_Component | Missing, duplicate, or unknown component | Artifact rejected |
| `payload_digest` | Digest of the actual exportable/importable payload | Bytes unavailable or digest mismatch | Import rejected |
| `provenance_digest` | Reviewed origin/attestation record for external inputs | Missing or untrusted origin | Security gate fails |

### Build and Import Counters

| Operation | Built artifact | Imported artifact | Rejection condition |
|---|---:|---:|---|
| Exact artifact construction | exactly 1 | 0 | More than one construction |
| Exact artifact import | 0 | exactly 1 | More than one import or import after build |
| Engine binary build | at most 1 | 0 | Duplicate build |
| CLI binary build | at most 1 | 0 | Duplicate build |
| Mandatory Go runtime content build | at most 1 | 0 | Duplicate build |
| Rust SDK content build | at most 1 | 0 | Duplicate build |
| Exact engine service start | exactly 1 after admission | exactly 1 after admission | Zero or more than one start |
| Installed Rust baseline materialization | exactly 1 | exactly 1 | Zero or more than one baseline |

## Sign-off Case Inventory Policy

| Case family | Minimum required coverage | Authority boundary |
|---|---|---|
| Common sdk-sdk lifecycle | All 17 subject checks | Does not prove client generation or general authoring completeness |
| Stable default connector | Exact version handshake, authenticated query, production distribution outcome, close, and child reap | Records verified download or exact PATH fallback honestly |
| Core generated API | Scalar, enum, input, object, interface, nullable, list-object, expected-type, and Void paths | Reuses Feature 4's complete generated surface |
| Feature 5 integration | Resolution, empty/existing/no-generate init, operations, checked/legacy runtime, generated-lock-toolchain negatives, path/ownership negatives, and redaction | Uses one shared service and baseline |
| Feature 6 module authoring | Packaged self-consumer plus constructor, sync, async, state, Core, self, dependency, interface, enum, default, error, panic, cancellation, and concurrent calls | Uses production TypeDef and dispatcher paths |
| Feature 7 standalone clients | Initialized local client, pinned remote dependency client, schema regeneration, Core query, and namespaced module query | Runs outside repository Cargo workspace and without path dependencies |
| Definitive Go client behaviour | Directory, Git, container, container mutation, list, and typed exec-error variants | Matches observable result through idiomatic Rust API |
| Feature 8 applicability catalog | Every remaining applicable integration assertion | Never invokes another language SDK merely because its test was authoritative |

## Platform Verification Policy

| Layer | Linux | macOS | Windows | Architecture policy |
|---|---|---|---|---|
| Archive descriptor | Required | Required | Required | Exhaustive amd64 and arm64 pure cases |
| Native process and PATH | Required Native_OS_Job | Required Native_OS_Job | Required Native_OS_Job | One native architecture per OS plus descriptor coverage for the other |
| Cache, atomic publication, and permissions | Unix-native | Unix-native | Windows ACL/path-native | Must use native filesystem semantics |
| Control line, diagnostics, redaction, and reap | Native | Native | Native | No engine required |
| Public API compile and docs | Rust 1.97.1 | Rust 1.97.1 | Rust 1.97.1 | Declared features and committed lockfiles |
| Exact-engine SDK_Signoff | Required for initial Linux/amd64 verdict | Separate future artifact/verdict | Separate future artifact/verdict | Evidence never widened across platform identity |

## Security Gate Policy

| Gate | Exact subject | Pass policy | Exception policy |
|---|---|---|---|
| Locked resolution | Every Rust workspace/example root admitted to support | Committed lockfile used with `--locked` | No generated lock drift |
| Cargo Deny | Complete public and private Rust dependency graph | Advisories, licenses, bans, and sources pass | Reviewed advisory/license exception only |
| Unsafe boundary | All production Rust source | Workspace deny remains effective | Separate narrow review, safety proof, and tests |
| Dependency automation | Every live Rust Cargo root | Correct Cargo ecosystem and update scope | No irrelevant ecosystem as substitute |
| External artifacts | Images, CLI archives, toolchains, scanner, and DB | Immutable digest/checksum plus reviewed provenance | Explicit origin exception only |
| Exact artifact scan | The same payload digest used by SDK_Signoff | No unexcepted high or critical OS/library finding | Finding-specific owner, rationale, remediation, expiry |
| Packaged-content boundary | Installed Rust SDK and module/client fixtures | No ambient repository path or mutable dependency | No exception for release evidence |
| Secret canaries | Files, images, cache keys, stdout/stderr, diagnostics, traces, reports, evidence, and verdict | No canary occurrence | No exception |
| Workflow permissions | Rust CI and sign-off automation | Minimum read-only permissions unless a step proves a narrower write need | Permission-specific review |

## Requirements

### Requirement 1: Exact Capability Scope and Applicability

**User Story:** As a release reviewer, I want every selected integration observation
accounted for explicitly, so that a large green number cannot hide foreign-language,
engine-only, or untested Rust behaviour.

#### Acceptance Criteria

1. WHEN Feature 8 implementation begins, THE contract tooling SHALL validate exactly
   1,081 existing Feature 8 Capability_IDs.
2. WHEN Feature 8 implementation begins, THE contract tooling SHALL validate existing
   scope digest `sha256:2969bd8fde19fc17d327cef637b9d848eca01040e88caffc09a4e9a4ad9bc5f9`.
3. WHEN Feature 8 implementation begins, THE contract tooling SHALL validate 1,072
   `Missing` integration rows and nine `Partial` Go-client rows.
4. WHEN the Feature 8 policy inventory is rendered, THE Canonical_Inventory SHALL
   contain every new Rust policy capability listed in this document.
5. THE applicability catalog SHALL contain exactly one Applicability_Record for every
   existing Feature 8 Capability_ID.
6. WHEN an applicable item is classified, THE Applicability_Record SHALL identify at
   least one Conformance_Assertion.
7. WHEN an exact-engine assertion is required, THE Applicability_Record SHALL identify
   at least one Case_Definition.
8. WHEN more than one Capability_ID maps to one assertion, THE Applicability_Record
   SHALL preserve every capability-local authority anchor.
9. WHEN a Foreign_SDK_Mechanism is classified `Inapplicable`, THE decision evidence
   SHALL name the exact foreign mechanism.
10. WHEN a foreign test contains a shared SDK invariant, THE applicability catalog
    SHALL route that invariant to a Rust Conformance_Assertion.
11. WHEN an Engine_Owned_Capability is classified `Inapplicable`, THE decision evidence
    SHALL prove the absence of a Rust input, output, lifecycle, or compatibility effect.
12. IF an item has a Rust-observable effect, THEN THE applicability catalog SHALL avoid
    classifying it as Engine_Owned_Capability.
13. IF an item lacks target-scoped Rust verification, THEN THE Completeness_Ledger
    SHALL retain a Blocking_Status.
14. WHEN an implementation defect is discovered by conformance review, THE
    applicability catalog SHALL route the defect to its owning Feature 2–7 capability.
15. WHEN an applicability source fingerprint changes, THE contract tooling SHALL
    reject the stale record.
16. WHEN the selected integration inventory adds or removes an item, THE contract
    tooling SHALL report the exact scope delta before accepting a new digest.
17. WHEN Feature 8 closes, THE existing 1,081-item scope SHALL contain no unjustified
    Blocking_Status.
18. WHEN Feature 8 closes, THE new Feature 8 policy scope SHALL contain no
    Blocking_Status.

### Requirement 2: Provider-Neutral Sign-off Host Preflight

**User Story:** As a maintainer, I want to prove the execution host before committing
hours to an exact engine build, so that infrastructure incompatibility fails quickly
and cannot masquerade as an SDK defect.

#### Acceptance Criteria

1. WHEN the first Feature 8 execution task begins, THE workflow SHALL evaluate
   Signoff_Host_Preflight before building an Exact_Target artifact.
2. THE Signoff_Host_Preflight implementation SHALL accept a provider-neutral
   Signoff_Host_Profile.
3. WHEN the first implementation validates the dedicated Namespace XL Linux/amd64
   host, THE preflight SHALL treat the provider identity as non-authoritative
   execution metadata.
4. WHEN the host platform differs from the run plan, THE preflight SHALL fail before
   artifact construction.
5. WHEN host CPU, memory, or workspace capacity is below the declared budget, THE
   preflight SHALL fail before artifact construction.
6. WHEN the container daemon is unavailable, THE preflight SHALL fail before pulling
   or building target content.
7. WHEN privileged Dagger engine operation is evaluated, THE preflight SHALL start one
   pinned prebuilt smoke engine.
8. WHEN the smoke engine becomes ready, THE preflight SHALL prove isolated service
   reachability.
9. WHEN the smoke observation completes, THE preflight SHALL stop and reap the smoke
   engine.
10. THE preflight SHALL avoid building the Exact_Target engine or CLI.
11. THE preflight SHALL avoid installing the Rust SDK.
12. THE preflight SHALL avoid executing a Case_Definition.
13. THE preflight SHALL avoid claiming a Capability_ID.
14. WHEN export/import support is evaluated, THE preflight SHALL round-trip a canary
    payload with an unchanged digest.
15. WHEN cache persistence is evaluated, THE preflight SHALL observe reuse of an
    identical canary build.
16. WHEN a preflight phase exceeds its declared bound, THE preflight SHALL fail with
    the exact phase identity.
17. WHEN the host profile or container daemon identity changes, THE workflow SHALL
    require a fresh preflight.
18. IF preflight fails, THEN THE workflow SHALL prevent SDK_Signoff from starting.
19. WHEN another provider satisfies the same profile, THE workflow SHALL permit that
    host without changing an SDK requirement.
20. WHEN preflight evidence is persisted, THE record SHALL exclude personal host paths,
    account identifiers, and real credentials.

### Requirement 3: Closed Conformance Case Catalog

**User Story:** As a release reviewer, I want the complete case inventory fixed before
the engine starts, so that expensive execution cannot silently omit difficult cases or
grow into unrelated SDK work.

#### Acceptance Criteria

1. WHEN SDK_Signoff is planned, THE case planner SHALL construct the complete
   Case_Catalog before artifact build or import.
2. THE Case_Catalog SHALL bind the Exact_Target digest.
3. THE Case_Catalog SHALL bind the Subject_Revision or source digest.
4. THE Case_Catalog SHALL bind the target platform.
5. THE Case_Catalog SHALL contain every applicable Feature 8 Conformance_Assertion.
6. THE Case_Catalog SHALL contain all 17 Common_Harness_Cases.
7. THE Case_Catalog SHALL exclude the sdk-sdk harness-self check from Rust subject
   evidence.
8. THE Case_Catalog SHALL contain Feature 3's stable-default-connector case.
9. THE Case_Catalog SHALL contain every Core path listed in the Core case-family policy.
10. THE Case_Catalog SHALL contain every Feature 5 case family listed in this document.
11. THE Case_Catalog SHALL contain every Feature 6 case family listed in this document.
12. THE Case_Catalog SHALL contain all five Feature 7 deferred sign-off cases.
13. THE Case_Catalog SHALL contain all nine selected definitive Go client behaviours.
14. WHEN a Case_Definition is admitted, THE case SHALL identify its complete
    Capability_ID scope.
15. WHEN a Case_Definition is admitted, THE case SHALL identify immutable fixture and
    assertion digests.
16. WHEN a Case_Definition is admitted, THE case SHALL identify its timeout,
    concurrency, network, and retry policy.
17. IF a case selector is unknown or duplicated, THEN THE case planner SHALL reject the
    catalog.
18. IF an applicable Capability_ID lacks a case route, THEN THE case planner SHALL
    reject the catalog.
19. IF a case claims an unrelated SDK capability, THEN THE case planner SHALL reject
    the catalog.
20. WHEN the same observable assertion covers multiple authority rows, THE case planner
    SHALL execute that assertion once per required fixture context.
21. THE Case_Catalog SHALL avoid running the complete Definitive_Go_SDK suite.
22. THE Case_Catalog SHALL avoid running another language SDK suite.
23. THE Case_Catalog SHALL avoid repository-wide generation.
24. WHEN a Case_Catalog input changes, THE workflow SHALL require a new catalog digest
    before sign-off.

### Requirement 4: Matching Engine-Free Closure Evidence

**User Story:** As a maintainer, I want sign-off to consume completed Rust-first work
instead of replaying it, so that the exact-engine run proves only boundaries that
direct models cannot prove.

#### Acceptance Criteria

1. WHEN SDK_Signoff is admitted, THE evidence gate SHALL require one
   Implementation_Closure_Bundle.
2. THE Implementation_Closure_Bundle SHALL bind the Exact_Target digest.
3. THE Implementation_Closure_Bundle SHALL bind the Subject_Revision or source digest.
4. THE Implementation_Closure_Bundle SHALL bind every consumed generated-asset digest.
5. THE Implementation_Closure_Bundle SHALL contain the applicable Feature 2 closure
   identity.
6. THE Implementation_Closure_Bundle SHALL contain the applicable Feature 3
   deterministic closure identity.
7. THE Implementation_Closure_Bundle SHALL contain the applicable Feature 4 closure
   identity.
8. THE Implementation_Closure_Bundle SHALL contain the applicable Feature 5 closure
   identity.
9. THE Implementation_Closure_Bundle SHALL contain the applicable Feature 6 closure
   identity.
10. THE Implementation_Closure_Bundle SHALL contain the Feature 7 closure identity.
11. THE Implementation_Closure_Bundle SHALL contain the complete Portable_Platform_Matrix
    result.
12. THE Implementation_Closure_Bundle SHALL contain the current Rust security and
    hygiene result.
13. IF any closure record is missing, failed, stale, or target-incompatible, THEN THE
    evidence gate SHALL prevent engine startup.
14. THE SDK_Signoff graph SHALL avoid replaying engine-free Rust unit suites.
15. THE SDK_Signoff graph SHALL avoid replaying engine-free fixture suites.
16. THE SDK_Signoff graph SHALL avoid replaying Cargo formatting, clippy, or rustdoc.
17. THE SDK_Signoff graph SHALL avoid replaying Cargo Deny.
18. THE SDK_Signoff graph SHALL avoid replaying direct Go ABI tests.
19. WHEN a closure identity changes, THE evidence gate SHALL require a new matching
    bundle.

### Requirement 5: One Exportable Exact-Target Artifact

**User Story:** As a release operator, I want one reusable target artifact, so that
every case and retry observes identical bytes without rebuilding Dagger.

#### Acceptance Criteria

1. WHEN SDK_Signoff starts for one target and platform, THE artifact pipeline SHALL
   build or import exactly one Exact_Target_Signoff_Artifact.
2. THE artifact manifest SHALL account for every field in the Artifact Manifest
   Contract.
3. WHEN an artifact is built, THE pipeline SHALL build the engine binary at most once.
4. WHEN an artifact is built, THE pipeline SHALL build the CLI binary at most once.
5. WHEN an artifact is built, THE pipeline SHALL build mandatory engine-packaged Go
   runtime content at most once.
6. WHEN an artifact is built, THE pipeline SHALL build Rust SDK content at most once.
7. WHEN an artifact is imported, THE pipeline SHALL avoid rebuilding any
   Artifact_Component.
8. WHEN an artifact is imported, THE pipeline SHALL verify every component digest.
9. WHEN an artifact is imported, THE pipeline SHALL verify the payload digest before
   engine startup.
10. THE Exact_Target_Signoff_Artifact SHALL retain the actual payload bytes required
    by the runner.
11. THE artifact pipeline SHALL avoid treating a digest string as recoverable content.
12. THE artifact pipeline SHALL exclude unrelated SDK builders.
13. THE artifact pipeline SHALL exclude unrelated SDK tests.
14. THE artifact pipeline SHALL exclude unrelated SDK generation.
15. THE artifact pipeline SHALL exclude distribution-wide build paths.
16. THE artifact pipeline SHALL avoid running the complete Go SDK test suite.
17. WHEN an artifact input changes, THE artifact pipeline SHALL derive a different
    artifact identity.
18. WHEN unchanged artifact bytes are reused after a host restart, THE artifact
    pipeline SHALL retain the same payload digest.
19. IF any build/import counter violates the counter policy, THEN THE evidence gate
    SHALL reject the artifact.
20. IF any external component lacks immutable identity or reviewed provenance, THEN THE
    security gate SHALL reject the artifact.

### Requirement 6: One Engine, One Rust Baseline, and Isolated Fan-out

**User Story:** As a release operator, I want every case to share one exact engine and
one installed baseline, so that sign-off is both efficient and internally consistent.

#### Acceptance Criteria

1. WHEN engine-backed cases begin, THE sign-off runner SHALL start exactly one
   exact-target engine service.
2. WHEN the engine service becomes ready, THE sign-off runner SHALL verify its target
   identity before case execution.
3. WHEN the Rust baseline is prepared, THE sign-off runner SHALL materialize exactly
   one Installed_Rust_Baseline.
4. THE Installed_Rust_Baseline SHALL install the Rust SDK only from the exact artifact
   dependency descriptor.
5. THE Installed_Rust_Baseline SHALL exclude ambient repository path dependencies.
6. THE Installed_Rust_Baseline SHALL place the exact artifact CLI on the case PATH.
7. WHEN the stable-default-connector case runs, THE runner SHALL leave explicit-local
   CLI selection unset.
8. WHEN the production checksum manifest is unavailable with 403 or 404, THE connector
   case SHALL observe the exact CLI through Compatibility_PATH_Fallback.
9. WHEN Compatibility_PATH_Fallback is observed, THE evidence SHALL avoid claiming a
   successful Verified_Download.
10. WHEN the production checksum manifest becomes available, THE connector case SHALL
    require the verified download path.
11. WHEN a case workspace is created, THE sign-off runner SHALL branch it from the
    Installed_Rust_Baseline.
12. THE sign-off runner SHALL assign a distinct workspace and environment namespace to
    every case.
13. THE sign-off runner SHALL bind every case to the same engine service.
14. THE sign-off runner SHALL enforce the catalog's bounded concurrency.
15. WHEN a case fails, THE sign-off runner SHALL preserve other case workspaces from
    mutation.
16. WHEN a case assertion fails, THE attempt history SHALL retain that failure even if
    a later attempt passes.
17. WHEN an infrastructure attempt is retried, THE sign-off runner SHALL reuse the
    same artifact, engine, and baseline.
18. IF an infrastructure retry would require a second engine start, THEN THE sign-off
    runner SHALL fail the current run.
19. IF a case accesses another case's mutable state, THEN THE isolation gate SHALL fail
    the run.
20. WHEN the case inventory completes or aborts, THE sign-off runner SHALL stop and
    reap the engine service.
21. IF the runner observes zero or multiple engine starts, THEN THE evidence gate SHALL
    reject the run.
22. IF the runner observes zero or multiple baseline materializations, THEN THE
    evidence gate SHALL reject the run.

### Requirement 7: Executable Go-Level Rust Conformance

**User Story:** As a Rust SDK adopter, I want observable parity with the definitive
SDK and target integration behaviour, so that completeness means usable Rust rather
than matching source structure.

#### Acceptance Criteria

1. WHEN a definitive Go test establishes an applicable observable edge case, THE Rust
   case SHALL verify the same result or a reviewed Idiomatic_Equivalent.
2. WHEN a Go source mechanism is not idiomatic Rust, THE Rust case SHALL preserve the
   observable contract without copying the mechanism.
3. WHEN a selected integration assertion is engine-owned but Rust-observable, THE Rust
   case SHALL exercise it through the public Rust client or module path.
4. WHEN a selected integration assertion is foreign-SDK-only, THE decision evidence
   SHALL avoid constructing that foreign SDK during sign-off.
5. WHEN sdk-sdk subject conformance runs, THE runner SHALL execute all 17 applicable
   checks against the exact packaged Rust SDK.
6. WHEN sdk-sdk subject conformance passes, THE evidence SHALL restrict its claims to
   mapped harness Capability_IDs.
7. WHEN standalone client conformance runs, THE fixture SHALL build outside the Dagger
   repository Cargo workspace.
8. WHEN a Rust module fixture runs, THE case inventory SHALL cover initialization,
   development, generation, loading, execution, and dependency use.
9. WHEN module dispatch runs, THE cases SHALL cover constructor, synchronous,
   asynchronous, stateful, Core, self, dependency, interface, enum, default, error,
   panic, cancellation, and concurrent behaviour.
10. WHEN generated Core conformance runs, THE cases SHALL cover every Core shape listed
    in the case-family policy.
11. WHEN the packaged self-consumer runs, THE fixture SHALL resolve SDK content only
    from the exact engine-packaged artifact.
12. WHEN the pinned remote-client case runs, THE fixture SHALL use an immutable remote
    revision without an ambient checkout path.
13. WHEN schema regeneration runs, THE case SHALL prove changed generated content and
    preserved authored content.
14. WHEN the Core client query runs, THE case SHALL execute through the public generated
    Rust API.
15. WHEN the namespaced module query runs, THE case SHALL execute through the generated
    bound-module Rust API.
16. WHEN schema or selected authority fixtures drift, THE conformance tooling SHALL
    report added, removed, and reclassified assertion scope.
17. IF any applicable assertion lacks a passing case outcome, THEN THE sign-off verdict
    SHALL fail.
18. WHEN the repository claims Go-level completeness, THE Completeness_Ledger SHALL
    contain sufficient evidence for every active applicable capability.

### Requirement 8: Native Platform Closure

**User Story:** As a Rust SDK adopter, I want the supported OS behaviours exercised on
their native systems, so that portable source does not hide process, path, permission,
or cleanup failures.

#### Acceptance Criteria

1. THE Portable_Platform_Matrix SHALL include one Native_OS_Job on Linux.
2. THE Portable_Platform_Matrix SHALL include one Native_OS_Job on macOS.
3. THE Portable_Platform_Matrix SHALL include one Native_OS_Job on Windows.
4. THE descriptor suite SHALL cover Linux amd64.
5. THE descriptor suite SHALL cover Linux arm64.
6. THE descriptor suite SHALL cover macOS amd64.
7. THE descriptor suite SHALL cover macOS arm64.
8. THE descriptor suite SHALL cover Windows amd64.
9. THE descriptor suite SHALL cover Windows arm64.
10. WHEN a Native_OS_Job runs, THE job SHALL exercise native PATH and executable
    discovery.
11. WHEN a Native_OS_Job runs, THE job SHALL exercise native cache publication and
    retention.
12. WHEN a Native_OS_Job runs, THE job SHALL exercise native path and symlink or reparse
    boundaries applicable to that OS.
13. WHEN a Native_OS_Job runs, THE job SHALL exercise native child startup,
    termination, and reaping.
14. WHEN a Native_OS_Job runs, THE job SHALL exercise control-line isolation,
    diagnostics, and redaction.
15. WHEN a Native_OS_Job runs, THE job SHALL use Rust 1.97.1 and committed lockfiles.
16. THE Native_OS_Job SHALL avoid constructing a Dagger engine.
17. THE Native_OS_Job SHALL avoid downloading or starting another SDK.
18. WHEN a platform result is admitted, THE evidence SHALL bind the exact OS,
    architecture, toolchain, source, and test identities.
19. IF a Native_OS_Job is skipped, stale, or failed, THEN THE Portable_Platform_Matrix
    SHALL fail.
20. WHEN the initial exact-engine sign-off runs, THE platform identity SHALL be
    Linux/amd64.
21. IF a later release claims another exact-engine platform, THEN THE release gate
    SHALL require a separate artifact and verdict for that platform.

### Requirement 9: Locked Dependency and Supply-Chain Security

**User Story:** As a security reviewer, I want every shipped and build-time component
locked and attributable, so that Go-level completeness does not expand the attack
surface invisibly.

#### Acceptance Criteria

1. WHEN a Rust root is checked, THE security pipeline SHALL resolve its committed
   lockfile with `--locked`.
2. WHEN the Rust dependency graph is evaluated, THE security pipeline SHALL run all
   Cargo Deny classes.
3. IF an active RustSec advisory is reachable, THEN THE security pipeline SHALL fail.
4. IF a dependency license is unapproved, THEN THE security pipeline SHALL fail.
5. IF a wildcard dependency is not an allowed local path edge, THEN THE security
   pipeline SHALL fail.
6. IF a registry or Git source is unknown, THEN THE security pipeline SHALL fail.
7. WHEN Rust production code is compiled, THE workspace SHALL retain `unsafe_code =
   "deny"`.
8. IF an unsafe exception is proposed, THEN THE security review SHALL require a narrow
   allow, documented invariant, and exercising tests.
9. WHEN dependency automation is evaluated, THE automation policy SHALL enumerate
   every supported Cargo root.
10. WHEN dependency automation is evaluated, THE automation policy SHALL avoid
    treating an inapplicable package ecosystem as Rust coverage.
11. WHEN an external image is used, THE artifact policy SHALL require an immutable
    digest.
12. WHEN an external archive is used, THE artifact policy SHALL require a verified
    checksum.
13. WHEN an external tool or image is admitted, THE security record SHALL identify its
    reviewed publisher provenance.
14. WHEN the vulnerability scanner runs, THE scanner identity SHALL be pinned by
    immutable digest.
15. WHEN the vulnerability scanner runs, THE security record SHALL bind its
    vulnerability-database identity.
16. WHEN the exact artifact is scanned, THE scanner SHALL inspect the same payload
    digest used by SDK_Signoff.
17. THE exact artifact scan SHALL avoid rebuilding the engine or SDK content.
18. IF the exact artifact contains an unexcepted high or critical vulnerability, THEN
    THE Artifact_Security_Report SHALL fail.
19. WHEN an exception is admitted, THE exception SHALL identify the exact advisory or
    finding.
20. WHEN an exception is admitted, THE exception SHALL identify reachability and impact
    rationale.
21. WHEN an exception is admitted, THE exception SHALL identify an owner and upstream
    remediation.
22. WHEN an exception is admitted, THE exception SHALL identify a machine-evaluable
    expiry condition.
23. WHEN an exception's expiry condition becomes true, THE security pipeline SHALL
    reject the stale exception.
24. WHEN the packaged self-consumer resolves dependencies, THE fixture SHALL avoid
    repository-relative and mutable dependency sources.
25. WHEN Rust CI runs, THE workflow SHALL retain minimum required token permissions.

### Requirement 10: Secret, Diagnostic, and Evidence Safety

**User Story:** As a security reviewer, I want sign-off to prove that credentials and
host identity cannot leak, so that the evidence itself is safe to retain and publish.

#### Acceptance Criteria

1. WHEN sign-off fixtures are prepared, THE security harness SHALL create a
   Secret_Canary_Set containing session, registry, Git, environment, trace, and URL
   credentials.
2. THE Secret_Canary_Set SHALL contain only non-production high-entropy values.
3. WHEN a case completes, THE leak scanner SHALL inspect case files.
4. WHEN a case completes, THE leak scanner SHALL inspect generated and packaged
   content.
5. WHEN a case completes, THE leak scanner SHALL inspect cache keys and provenance.
6. WHEN a case completes, THE leak scanner SHALL inspect stdout and stderr.
7. WHEN a case completes, THE leak scanner SHALL inspect errors and Debug output.
8. WHEN a case completes, THE leak scanner SHALL inspect diagnostics and traces.
9. WHEN evidence is rendered, THE leak scanner SHALL inspect reports and verdicts.
10. IF any canary occurs in an inspected output, THEN THE security gate SHALL fail the
    complete sign-off run.
11. WHEN evidence is persisted, THE renderer SHALL omit absolute host paths.
12. WHEN evidence is persisted, THE renderer SHALL omit personal account and host
    identifiers.
13. WHEN diagnostics are retained, THE sign-off runner SHALL enforce declared size
    bounds.
14. WHEN a failure source is preserved, THE public rendering SHALL remain redacted.
15. THE exact artifact SHALL exclude live credentials.
16. THE Atomic_Signoff_Verdict SHALL exclude live credentials and Secret_Canary_Set
    values.
17. IF redaction cannot be proven, THEN THE security gate SHALL prevent evidence
    admission.

### Requirement 11: Engine-Free Feature 8 Checkpoints and Bounded Work

**User Story:** As a maintainer, I want Feature 8 orchestration developed through fast
Rust-first models, so that sign-off engineering does not recreate the multi-hour
test/build cycle it is intended to eliminate.

#### Acceptance Criteria

1. THE Feature 8 local checkpoint SHALL run without constructing a Dagger engine.
2. THE Feature 8 local checkpoint SHALL run without invoking a Dagger module.
3. THE Feature 8 local checkpoint SHALL run without building or testing another SDK.
4. THE Feature 8 local checkpoint SHALL run without repository-wide generation.
5. THE Feature 8 local checkpoint SHALL exercise production applicability logic through
   direct Rust fixtures.
6. THE Feature 8 local checkpoint SHALL exercise production artifact planning and
   counters through direct Rust fixtures.
7. THE Feature 8 local checkpoint SHALL exercise production case planning and
   isolation state through direct Rust fixtures.
8. THE Feature 8 local checkpoint SHALL exercise production platform and security
   admission through direct Rust fixtures.
9. THE Feature 8 local checkpoint SHALL exercise production verdict rendering through
   direct Rust fixtures.
10. THE Feature 8 local checkpoint SHALL reuse checked generated assets when owning
    inputs are unchanged.
11. WHEN a generation input changes, THE checkpoint SHALL regenerate only the affected
    Rust-owned artifact.
12. THE Feature 8 local checkpoint SHALL record commands and elapsed phase timings.
13. THE Feature 8 local checkpoint SHALL record generated-asset reuse decisions.
14. THE Feature 8 local checkpoint SHALL record Cargo invocation counts.
15. WHEN a checkpoint phase exceeds its reviewed budget, THE checkpoint SHALL terminate
    that phase with a distinct timeout result.
16. IF a proposed checkpoint requires an engine, THEN THE exception record SHALL prove
    why the production contract cannot be modeled directly.
17. IF a proposed checkpoint requires an engine, THEN THE exception SHALL require
    explicit approval before execution.
18. THE Signoff_Host_Preflight SHALL remain the only approved pre-sign-off engine
    infrastructure exception in this specification.
19. THE Feature 8 Implementation_Closure SHALL require format, locked check/test,
    warning-denied clippy, warning-denied rustdoc, Cargo Deny, source policy, evidence,
    and Portable_Platform_Matrix gates.
20. THE Feature 8 Implementation_Closure SHALL avoid claiming SDK_Signoff.

### Requirement 12: Atomic Digest-Bound Sign-off Verdict

**User Story:** As a release reviewer, I want one complete, inspectable verdict, so that
no partial pass, stale retry, or hidden rebuild can authorize the Rust SDK release.

#### Acceptance Criteria

1. WHEN SDK_Signoff completes, THE verdict renderer SHALL emit exactly one
   Atomic_Signoff_Verdict.
2. THE Atomic_Signoff_Verdict SHALL bind the Exact_Target digest.
3. THE Atomic_Signoff_Verdict SHALL bind the Subject_Revision or source digest.
4. THE Atomic_Signoff_Verdict SHALL bind the platform identity.
5. THE Atomic_Signoff_Verdict SHALL bind the Signoff_Host_Profile and preflight digest.
6. THE Atomic_Signoff_Verdict SHALL bind the Exact_Target_Signoff_Artifact manifest and
   payload digests.
7. THE Atomic_Signoff_Verdict SHALL bind the Implementation_Closure_Bundle digest.
8. THE Atomic_Signoff_Verdict SHALL bind the Case_Catalog digest.
9. THE Atomic_Signoff_Verdict SHALL bind the Portable_Platform_Matrix digest.
10. THE Atomic_Signoff_Verdict SHALL bind the Artifact_Security_Report digest.
11. THE Atomic_Signoff_Verdict SHALL contain every build and import counter.
12. THE Atomic_Signoff_Verdict SHALL contain the engine-start counter.
13. THE Atomic_Signoff_Verdict SHALL contain the baseline-materialization counter.
14. THE Atomic_Signoff_Verdict SHALL contain every Case_Attempt outcome.
15. THE Atomic_Signoff_Verdict SHALL record artifact build or import duration.
16. THE Atomic_Signoff_Verdict SHALL record engine startup duration.
17. THE Atomic_Signoff_Verdict SHALL record Rust SDK installation duration.
18. THE Atomic_Signoff_Verdict SHALL record exact-artifact security-scan duration.
19. THE Atomic_Signoff_Verdict SHALL record every case duration.
20. IF a required case is missing, THEN THE Atomic_Signoff_Verdict SHALL fail.
21. IF a required case is skipped or unknown, THEN THE Atomic_Signoff_Verdict SHALL
    fail.
22. IF a required assertion fails, THEN THE Atomic_Signoff_Verdict SHALL fail.
23. IF an admitted input is stale or target-incompatible, THEN THE
    Atomic_Signoff_Verdict SHALL fail.
24. IF a duplicate artifact build or import is observed, THEN THE
    Atomic_Signoff_Verdict SHALL fail.
25. IF a duplicate engine start is observed, THEN THE Atomic_Signoff_Verdict SHALL fail.
26. IF a duplicate Rust baseline is observed, THEN THE Atomic_Signoff_Verdict SHALL
    fail.
27. IF an unrelated SDK or distribution path is entered, THEN THE
    Atomic_Signoff_Verdict SHALL fail.
28. IF a security or platform gate fails, THEN THE Atomic_Signoff_Verdict SHALL fail.
29. IF any Secret_Canary_Set value leaks, THEN THE Atomic_Signoff_Verdict SHALL fail.
30. WHEN the verdict fails, THE evidence registry SHALL avoid admitting a successful
    subset.
31. WHEN the verdict passes, THE evidence registry SHALL enumerate only Capability_IDs
    proved by passed assertions or justified decisions.
32. WHEN verdict admission changes a status, THE completeness renderer SHALL derive
    the transition through Feature 1 policy.
33. WHEN derived reports are rendered, THE verification pipeline SHALL require a clean
    reproducible diff.
34. WHEN Feature 8 closes, THE final report SHALL distinguish implementation closure,
    platform closure, security closure, and exact-engine sign-off.
35. WHEN Feature 9 evaluates release readiness, THE release gate SHALL require the
    admitted passing Feature 8 verdict.

## Out of Scope

- Publishing crates, synchronizing final release versions, or changing stable release
  presentation; Feature 9 owns those actions.
- Running the complete Definitive Go SDK, target Dagger integration, or another
  language SDK test suite inside Rust sign-off.
- Treating Namespace as a required SDK dependency, permanent CI provider, behavioural
  authority, or evidence source.
- Claiming macOS or Windows exact-engine sign-off from the initial Linux/amd64 verdict.
- Replacing child-feature implementation closure with engine-backed tests.
- Reopening generated Core, transport, engine adapter, module authoring, or standalone
  client design unless conformance exposes a concrete defect.
- Accepting a repository-relative Rust SDK dependency as packaged sign-off evidence.
- Treating a passing retry as erasure of an earlier assertion failure.
- Treating the preflight smoke engine as SDK conformance evidence.

## Iteration and Feedback Notes

- Requirements-first feature workflow selected. Design and tasks remain consent-gated.
- The first implementation task will build the provider-neutral preflight contract and
  validate it on the dedicated Namespace XL devbox before target-artifact work begins.
- The Namespace discovery already demonstrated the value of preflight: the host has
  ample compute, persistent storage, and Docker, but ambient Go is 1.25.3 rather than
  the sign-off graph's pinned 1.26.1. Artifact-contained toolchains therefore govern.
- The 1,081-row scope is intentionally not translated one-for-one into 1,081 expensive
  engine invocations. Capability-local records may share exact assertions and fixtures,
  but they cannot share away semantic differences.
- The current sdk-sdk harness is authoritative for its 17 subject checks and explicitly
  does not cover `initClient`; Feature 7's five cases remain independent and mandatory.
- Feature 3's open live connector task can be satisfied honestly while beta.10 remains
  unpublished: the stable default connector observes the definitive 403/404 fallback
  and selects the exact built CLI from PATH. That closes a live default-connector claim,
  not a verified-download claim.
- The exact artifact must survive a host restart as bytes, not merely as a digest or an
  in-memory Dagger graph object. This is the key difference between development cache
  reuse and reproducible sign-off reuse.
- The final sign-off is intentionally strict: a case assertion that fails once makes
  the run fail. Infrastructure retries remain visible and may not create a second
  engine or baseline.
