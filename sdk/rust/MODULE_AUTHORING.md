# Rust module authoring and verification

This guide records the durable authoring, generation, dispatch, local-checkpoint, and
completed-engine verification contracts for the Rust SDK. The public syntax is ordinary
typed Rust; the engine adapter and generated support remain implementation details.

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

## Development build and test: engine-free and Rust-first

During implementation, run the narrowest owning package or fixture. The local
development loop uses Cargo directly from `sdk/rust` and the direct Go ABI package under
`sdk/rust/runtime`. It exercises the production source compiler, descriptor,
projections, registration, codecs, context, dispatcher, entrypoint adapter, and
result sink with Rust values and a recording transport.

The development contract is strict:

- no Dagger engine process, CLI/module invocation, or network-backed engine graph;
- no unrelated SDK builder, test, generation, or distribution-wide build;
- checked Core and module assets unless an owning digest changed;
- one scoped regeneration decision when change genuinely requires it;
- locked package checks, all module properties, bounded compile fixtures, direct
  adapter tests, formatting, Clippy, rustdoc, Cargo Deny, public package contents, and
  byte-clean output.

The focused test slices are:

```console
cargo test -p dagger-sdk-engine \
  --test initialization_properties \
  --test runner_properties --locked
cargo test -p dagger-codegen \
  --test module_authoring_assets \
  --test module_diagnostics --locked
cargo test -p dagger-sdk --test module_authoring_compile --locked
```

The complete local gate additionally runs the canonical workspace format, check, test,
warning-denied Clippy/rustdoc, Cargo Deny, package, direct Go ABI, generated
drift/ownership, and clean-output checks once. It does not continuously
regenerate checked assets.

If a proposed local check genuinely requires an engine, keep the direct model gap
explicit and add the smallest focused engine-backed regression test that owns that
boundary. The ordinary release-readiness check is intentionally smaller: package the
two public crates, assemble the complete engine with the Rust SDK content, and run one
isolated external Rust consumer against that completed engine. This verifies the
packaged module-authoring path without replacing the faster compiler and dispatch
tests above. Packaging and complete-engine assembly create local artifacts only; any
manual GitHub Release requires separate, direct authorization.
