# dagger-codegen

`dagger-codegen` is the schema compiler used to build the generated client surface of
the [Dagger Rust SDK](https://github.com/dagger/dagger/tree/main/sdk/rust). It is
development tooling for the SDK rather than an application-facing client library.
Application authors should depend on
[`dagger-sdk`](https://crates.io/crates/dagger-sdk).

The crate owns the pure part of generation:

- validating the reviewed Dagger, engine, schema, Go SDK, `sdk-sdk`, Rust SDK, and
  toolchain identities;
- checking that bounded introspection bytes match the target's SHA-256 digest;
- compiling raw introspection into an ordered canonical schema with exact GraphQL
  wire names, recursive nullability, defaults, directives, documentation, and source
  coordinates;
- projecting that schema into collision-checked Rust 2024 names, wrapper-correct
  types, required/omittable argument plans, typed directive and enum-alias policies,
  and one total field execution strategy per coordinate;
- cataloging 1,661 exact-target semantic bindings, including explicit no-symbol
  containment for target-private metadata, with domain-separated implementation
  fingerprints;
- producing deterministic in-memory candidate artifacts and structured diagnostics.

It deliberately has no filesystem, process, network, engine-session, publication, or
completeness-ledger authority. Repository input discovery, formatting, comparison,
and transactional publication belong to `dagger-bootstrap`; runtime query execution
belongs to `dagger-sdk`.

## Correctness contract

Generation is fail-closed. A target identity, schema digest, reference, wrapper,
default, directive, or exact coordinate-inventory mismatch produces sorted typed
diagnostics and no projection plan. Rendering accepts only an immutable validated
plan through the exact-target `render_core` boundary, so raw transport data cannot be
reinterpreted by that pipeline's templates.

Canonical maps and sets are ordered by their exact GraphQL wire identities. Equivalent
introspection input order therefore produces byte-identical candidate output. The
crate performs no repository writes; callers must complete all validation, formatting,
completeness, and ownership checks before publishing generated files.

## Development status

`dagger-codegen` is published in version lockstep with the Rust SDK so Dagger's own
tooling can use an exact compatible release. Its API is still being stabilized as the
generated Rust client reaches complete target coverage. It is not a supported
replacement for `dagger-sdk`.

Contributors should read the Rust SDK
[`CONTRIBUTING.md`](https://github.com/dagger/dagger/blob/main/sdk/rust/CONTRIBUTING.md)
and
[`ARCHITECTURE.md`](https://github.com/dagger/dagger/blob/main/sdk/rust/ARCHITECTURE.md)
before changing schema, projection, rendering, or publication boundaries.
