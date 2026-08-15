# Dagger Rust SDK workspace

This workspace contains Dagger's beta Rust SDK, code generator, module-authoring
runtime, standalone-client generator, and engine integration. The current SDK version
is `1.0.0-beta.11.rust.1` and targets
the Dagger `v1.0.0-beta.11` engine contract.

Application users normally depend on [`dagger-sdk`](crates/dagger-sdk). The client is
owned and asynchronous: it supports generated and raw GraphQL operations,
caller-supplied connections, exact CLI provisioning, existing `dagger run` sessions,
typed failures, diagnostics, W3C context propagation, and explicit shared shutdown.

```rust,no_run
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = dagger_sdk::connect().await?;
    println!("Dagger {}", client.query().version().await?);
    client.close().await?;
    Ok(())
}
```

The SDK is distributed as repository release artifacts, not through crates.io. See
[`crates/dagger-sdk/README.md`](crates/dagger-sdk/README.md) for artifact installation
and application usage.

| Capability | Current boundary |
| --- | --- |
| Client API | Generated Core types and raw GraphQL over one owned asynchronous session |
| Connection safety | Exact engine version/revision checks, checksum-verified CLI downloads, loopback-only authenticated transport, and credential-safe diagnostics |
| Module support | Typed authoring attributes, descriptor generation, registration, dispatch, cancellation, and single-result publication |
| Standalone clients | Complete Core plus at most one independently pinned module, reusing the public client lifecycle |
| Engine integration | Rust-owned generation/runtime policy composed into the complete Dagger engine |

Module authors should start with [`MODULE_AUTHORING.md`](MODULE_AUTHORING.md).
Standalone-client users should read [`CLIENT_GENERATION.md`](CLIENT_GENERATION.md).
[`ARCHITECTURE.md`](ARCHITECTURE.md) records ownership and safety boundaries, while
[`CONTRIBUTING.md`](CONTRIBUTING.md) and [`MAINTAINING.md`](MAINTAINING.md) contain the
pinned development and maintenance procedures.

A module exports ordinary typed Rust explicitly:

```rust,no_run
#[dagger_sdk::object(root)]
pub struct Greeter;

#[dagger_sdk::methods]
impl Greeter {
    #[dagger(function)]
    pub fn greet(&self, name: String) -> String {
        format!("Hello, {name}!")
    }
}
```

Small crate examples can be run from this directory:

```bash
cargo run -p dagger-sdk --example first-pipeline
```

The standalone application examples under [`examples`](examples) each have their own
manifest; their exact commands are listed in [`examples/README.md`](examples/README.md).

## Development and release builds

Normal SDK development is engine-free and Rust-first. Run Cargo checks, generated-file
checks, and the direct Rust-owned Go ABI tests without constructing an engine. Add a
focused engine-backed regression only when the boundary cannot be represented by the
direct production harness.

Release assembly is separate. The repository's ordinary Rust SDK build packages
exactly `dagger-sdk-macros` and `dagger-sdk`, builds the Rust SDK engine content, and
composes it into a complete `linux/amd64` Dagger engine. Its verification step unpacks
the two packages and runs one isolated external Rust consumer against that completed
engine. It creates no tag or release and publishes no crate. Making those artifacts
available through a manual GitHub Release requires separate, direct authorization.
The complete builder and retrieval procedure is the
[`dagger-rust-builder-xl` Namespace runbook](NAMESPACE_BUILD.md).
