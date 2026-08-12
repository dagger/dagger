# Rust module authoring and verification

This guide records the durable authoring, generation, dispatch, local-checkpoint, and
SDK-sign-off contracts for the Rust SDK. The public syntax is ordinary typed Rust; the
engine adapter and generated support remain implementation details.

## Authoring surface

Exports are explicit. Rust visibility still determines whether generated sibling code
can access an item, but `pub` alone never exports it to Dagger. `dagger-sdk` re-exports
the exact-version attributes; users do not depend on `dagger-sdk-macros` directly.

```rust
#[dagger_sdk::object(root, rename = "greeter")]
pub(crate) struct Greeter {
    #[dagger(field)]
    greeting: String,
    #[dagger(state)]
    private_revision: i64,
}

#[dagger_sdk::methods]
impl Greeter {
    #[dagger(constructor)]
    fn new(greeting: String) -> Self {
        Self {
            greeting,
            private_revision: 0,
        }
    }

    #[dagger(function)]
    fn greet(&self, #[dagger(default = "world")] name: String) -> String {
        format!("{}, {name}", self.greeting)
    }
}
```

`object`, `interface`, `enum_type`, and `scalar` mark exported type contracts;
`methods` checks constructors and functions. Nested `field`, `state`, `constructor`,
`function`, `context`, defaults, target metadata, documentation, deprecation, and
renames are interpreted once by the shared authoring grammar. Unsupported shapes fail
at authored coordinates rather than degrading to untyped JSON.

Persistent state is owned data reconstructed for the selected object. A public field
appears in the TypeDef; a private persistent field participates only in state encoding;
an ordinary unmarked field remains implementation detail. Generated Core, self, and
dependency handles preserve their typed IDs and re-enter through the active session.

## Descriptor and generated ownership

The source compiler walks the selected Cargo package, respects the compilation cfg,
resolves the transitive local type closure, and produces one canonical descriptor.
Registration, introspection, codecs, the dispatch registry, and the module entrypoint
are projections of that descriptor. Procedural macros emit only typed crate-local
access and invocation bridges, so authors never maintain a parallel schema or switch
statement.

Generated assets carry exact target, source, visible-schema, generator, descriptor,
and content identities. Checked mode compares committed assets directly. Regeneration
is permitted only when one of those owning inputs changes, selects only the affected
asset domain, and replaces or removes only paths proven by the prior compatible
manifest. User-owned or unknown bytes are preserved and diagnosed.

## Dispatch and errors

The entrypoint reads an active call once into a typed envelope. An empty parent name
selects registration; an empty function name remains the root constructor selector.
For invocation, parent state and the complete named argument set are validated before
user code runs. Context parameters are injected and absent from TypeDefs.

Each call owns its parent, decoded values, session context, cancellation signal, and
single-assignment result sink. Successful values and unit, structured application
errors, contained panics, cancellation, encoding failures, publication failures, and
session-close failures remain distinct. The first terminal election is immutable;
cancellation or publication failure never triggers a second result path. Diagnostics
retain authored and wire coordinates plus typed safe causes without rendering tokens,
credential-bearing URLs, arbitrary panic payloads, host paths, or unbounded values.

## Checkpoint build and test: engine-free and Rust-first

During implementation, run the narrowest owning package or fixture. The Feature-end
checkpoint uses Cargo directly from `sdk/rust` and the direct Go ABI package under
`sdk/rust/runtime`. It exercises the production source compiler, descriptor,
projections, generated registry, codecs, context, dispatcher, entrypoint adapter, and
result sink with Rust values and a recording transport.

The checkpoint contract is strict:

- no Dagger engine process, CLI/module invocation, or network-backed engine graph;
- no unrelated SDK builder, test, generation, or distribution-wide build;
- checked Core and module assets unless an owning digest changed;
- one scoped regeneration decision when change genuinely requires it;
- locked package checks, all module properties, bounded compile fixtures, direct
  adapter tests, formatting, Clippy, rustdoc, Cargo Deny, repository Rust security,
  public package contents, derived reporting, and byte-clean output; and
- a typed record of every action, elapsed time, result, and generated-asset decision.

The focused evidence slices are:

```console
cargo test -p dagger-sdk-engine --test checkpoint_properties --locked
cargo test -p dagger-sdk-completeness \
  --test module_authoring_properties \
  --test module_authoring_evidence --locked
cargo test -p dagger-codegen \
  --test module_authoring_assets \
  --test module_diagnostics --locked
cargo test -p dagger-sdk --test module_authoring_compile --locked
```

The complete Feature-end gate additionally runs the canonical workspace format,
check, test, warning-denied Clippy/rustdoc, Cargo Deny, security, package, direct Go ABI,
generated drift/ownership, derived-report, and clean-output checks once. It does not
continuously regenerate checked assets.

If a proposed local check appears to require an engine, stop. Record the exact
unmodellable contract, evidence that the production direct model is insufficient, and
the smallest proposed sign-off case. Explicit approval records the case for sign-off;
it does not authorize an engine inside the local checkpoint.

## SDK sign-off: bounded exact-target evidence

SDK sign-off is separate from implementation closure. It consumes matching closure
evidence and does not replay engine-free compiler, fixture, hygiene, or security work.
Its invariant is:

- one reusable exact-target artifact;
- engine, CLI, mandatory Go runtime, and Rust content built at most once;
- one engine service and one installed Rust baseline;
- no unrelated SDK builders, tests, generation, or distribution builds;
- isolated case workspaces branched from the installed baseline;
- one digest-bound atomic verdict with artifact/import, engine-start, Rust-install,
  and per-case timings; and
- rejection of duplicate artifact construction, engine starts, baseline installation,
  stale identities, skipped/failed/missing cases, or overbroad claims.

The closed Feature 6 inventory is `registration`, `constructor-state`,
`execution-shapes`, `types`, `handles-context`, `negative-dispatch`,
`concurrency-cancellation`, `packaged-self-consumer`, and `common-harness`. The packaged
self-consumer is a Rust-authored Dagger module that resolves only the exact
engine-packaged Rust SDK and uses its generated Core surface to run a bounded Rust SDK
workflow; a repository-relative or unpackaged SDK dependency fails the case.

The common harness retains only its declared lifecycle authority. A smoke case closes
only its enumerated engine capability and cannot replace source, type, dispatch,
fixture, security, or implementation-closure evidence. Feature 8 expands this bounded
matrix into complete platform conformance, and Feature 9 owns published-release
self-hosting.
