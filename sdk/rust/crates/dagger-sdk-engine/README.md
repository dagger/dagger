# Dagger Rust engine integration

`dagger-sdk-engine` is the private Rust operation tool packaged with the Dagger
engine. It owns canonical operation models, immutable Cargo project adoption,
generated-file ownership, confined execution, and failure-atomic operation
publication. Runtime verification and the thin engine adapter are built in subsequent
integration work.

The packaged `dagger-rust-engine execute` command accepts only a canonical operation
request, exact visible schema, immutable engine descriptor, and explicit project root.
It invokes no caller-selected command or shell. Existing generated files may be
replaced only when a compatible operation manifest proves their current digests, and
that manifest becomes visible only after every artifact and bounded post-work action
succeeds.

This crate is repository tooling rather than an application dependency. It is not
published, and generated user projects depend only on the public `dagger-sdk` crate.

The crate also owns the typed local-checkpoint planner. Its validated form can contain
only Rust SDK package actions, direct Rust-owned Go ABI tests, generated ownership,
security, package, and clean-output checks. Engine, Dagger, network-graph, unrelated
SDK, unscoped-generation, and distribution proposals are rejected before they can be
rendered as executable work. See [`../../MODULE_AUTHORING.md`](../../MODULE_AUTHORING.md)
for the boundary between this local record and deferred SDK sign-off.

For standalone clients, this crate also owns confined Cargo discovery, semantic
amendments, workspace client preflight, manifest-authorized failure-atomic publication,
and exact Cargo-process accounting. The planner reuses checked assets and one fixture
SDK baseline when their owning inputs are current. Its consumer and contributor
contract is in [`../../CLIENT_GENERATION.md`](../../CLIENT_GENERATION.md).

Licensed under Apache-2.0 as part of the Dagger repository.
