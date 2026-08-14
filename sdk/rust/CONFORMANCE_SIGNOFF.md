# Rust SDK conformance and exact-target sign-off

This runbook is the durable operator boundary for Feature 8. It keeps routine Rust
implementation work engine-free, then performs one deliberately bounded Linux/amd64
exact-target observation. A passing result is evidence for Feature 9; it is not a
release, publication decision, or claim for another exact-engine platform.

## What each phase proves

| Phase | Runs an engine? | Proves |
| --- | --- | --- |
| Applicability review | No | Every selected Go and integration capability has one reviewed Rust disposition and evidence route. |
| Implementation closure | No | The Rust implementation, fixtures, generated assets, hygiene, documentation, and ordinary security gates are current. |
| Native platform evidence | No | Native Rust behaviour on Linux, macOS, and Windows plus the pure release-descriptor matrix. |
| Host preflight | One isolated smoke engine | The replaceable sign-off host can persist, export/import, cache, start, reach, stop, and reap the required infrastructure. It proves no SDK capability. |
| Exact-target sign-off | One orchestration invocation and one exact-target engine | The complete closed Rust case catalog passes against one imported artifact and one installed Rust baseline. |
| Release handoff | No additional work | The exact retained bundle and payload bytes, security report, subject, platform, and passing verdict are available to Feature 9. |

The **Orchestration_Engine** evaluates the Dagger construction graph. The
**Exact_Target_Engine** is the built product under test. Their identities and start
counts are separate; neither may be inferred from the other.

## 1. Review and reproduce applicability

Run the checked applicability compiler from `sdk/rust`. It performs no discovery and
must produce a clean diff:

```console
cargo run -p dagger-sdk-completeness --bin dagger-conformance-applicability --locked -- \
  --ledger completeness/artifacts/ledger.json \
  --scope completeness/conformance-scope.json \
  --review completeness/conformance-applicability-review.json \
  --output completeness/conformance-applicability.json \
  --audit completeness/conformance-applicability-audit.json

cargo run -p dagger-sdk-completeness --bin dagger-conformance-catalog --locked -- \
  --root ../..
```

Inspect disposition counts and every changed identity. `Inapplicable` means a reviewed
engine-owned or foreign-SDK boundary; it must never be reported as Rust implementation.

## 2. Build engine-free implementation closure

Use the production checkpoint planner and execute only stale owning domains. The final
engine-free gate includes formatting, locked checks and tests, warning-denied Clippy
and rustdoc, Cargo Deny, source policy, direct Rust-owned Go adapter tests, native
observations, documentation, and clean generated output. It must record commands,
elapsed time, Cargo invocation counts, and reused generated assets.

No checkpoint command may invoke Dagger, construct an engine, execute a module, build
or test another SDK, scan a target image, enter a distribution graph, or run unscoped
generation. A proposed exception stops the checkpoint and requires a model-gap record
and explicit approval; a convenient engine fallback is not closure evidence.

## 3. Collect native evidence

Linux and macOS use the routine `Rust SDK Development Platforms` workflow or the same
native producer locally:

```console
./scripts/ci-platform-preflight.sh <observation-output.json>
```

Windows is refreshed only for ultimate SDK sign-off through the separately dispatched
`Rust SDK Windows Preflight` workflow. Each job uploads one bounded canonical
observation. The Rust aggregator requires exactly one current observation per OS with
the same source, toolchain, target, and test identities; missing, stale, duplicate,
skipped, or failed evidence rejects the portable matrix.

## 4. Admit the replaceable host

Run `dagger-rust-sdk-signoff preflight` with the checked Linux/amd64 profile on a clean
dedicated host. A Namespace devbox is one suitable example, not a repository or SDK
dependency. Use placeholders such as `<signoff-host>`, `<candidate-root>`, and
`<persistent-output>` in copied instructions; do not record account, box, or personal
filesystem identities.

The preflight records host resources, container daemon and storage identities, one
pinned smoke tool/engine, persistence, export/import, cache reuse, timings, canary
inspection, and exact start/ready/stop/reap counts. Stop the smoke engine before any
target artifact work. Reuse the record only while its profile and all owning inputs
remain current.

## 5. Build and retain one exact-target artifact

Start from a clean committed, reachable subject revision. In the artifact-producing
invocation, build the exact Linux/amd64 engine, CLI, mandatory Go runtime content, and
Rust content at most once. Do not run the Go SDK suite. Export the canonical outer
bundle to persistent host storage and end the producing session without starting the
Exact_Target_Engine.

The retained layout is:

```text
rust-sdk-signoff-<payload-digest>.tar
├── manifest.json
├── provenance.json
├── engine.oci.tar.zst
└── checksums.sha256
```

Record construction/component counts, manifest and payload digests, outer bundle
digest, toolchain/provenance identities, and phase timing. A digest without the actual
outer and inner bytes is not a reusable artifact.

## 6. Restart, import, scan, and sign off

Use a fresh orchestration invocation. Supply the retained outer bundle to the Import
plan; permit zero component builds and exactly one verified container import. Scan the
same retained payload and record the immutable scanner image, vulnerability database,
publisher/provenance, findings, exceptions, policy result, and scan timing.

Only after Rust admission succeeds may the graph start one Exact_Target_Engine and
materialize one installed Rust baseline. Fan out the complete closed catalog with the
reviewed concurrency, timeout, network, workspace, cache, environment, and retry
policies. Assertion failure is terminal; only named infrastructure failures may retry,
and every attempt remains in evidence. Stop and reap the engine once on success or
failure.

Admission also requires one current Rust-first manifest entry for every selected pinned
Go integration scenario. The Go source supplies immutable provenance and may scaffold
a candidate, but the executable contract is a small language-neutral scenario spine
bound to exactly one generated public-Core realization or reviewed idiomatic Rust
fixture. There is no generic SDK backend. Foreign SDK fixtures, unreviewed candidates,
ambiguous or stale authority mappings, and boundary reachability substitutions such as
`dagger version` are rejected. Until the Rust realization set is total, admission stops
before artifact construction or engine startup and reports verification mapping drift
rather than inferred SDK incompleteness.

The invocation must report all raw identities, attempts, counters, timings, cleanup
results, and forbidden-event counts to the Rust verdict model. Go collects graph
observations; it does not compute a pass. The Rust model emits one passed or failed
atomic verdict and never a successful subset.

## 7. Inspect failures and reproduce reports

Start at the first typed diagnostic and its semantic phase. Preserve both a case or
preflight failure and any cleanup failure. Durable output contains stable IDs and safe
repository-relative coordinates only; raw subprocess output remains ephemeral and
canary-scanned.

After a pass, derive status changes through the Feature 1 transition API, regenerate
the ledger and neutral reports, and rerun the read-only generators. The diff must be
clean on a second run. Reports keep applicability, implementation, native platform,
security, and exact-engine phases independent and retain any unsupported blocker.

## 8. Retain the Feature 9 handoff

Derive one `ReleaseHandoffRecord` only from the passing authoritative Import verdict
and the still-available verified bundle. It binds the outer bundle, inner payload,
manifest, security report, verdict, subject revision, and Linux/amd64 platform.

Its authority is `evidence-only`. Feature 9 may copy these exact bytes or add
release-only metadata around them, but may not rebuild, recompress under the same
identity, widen the platform, tag, publish, or infer release readiness from the
handoff alone. Another platform requires its own artifact, security report, passing
verdict, and handoff.
