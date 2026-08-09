# Dagger Rust SDK workspace

This workspace contains Dagger's Rust SDK, code generator, bootstrap utility, and the
machine-checked completeness contract used to measure the SDK against its pinned
authorities.

Application users normally depend on [`dagger-sdk`](crates/dagger-sdk). The stable
client is owned and asynchronous: it supports generated and raw GraphQL operations,
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

Install the published SDK with `cargo add dagger-sdk`. See
[`crates/dagger-sdk/README.md`](crates/dagger-sdk/README.md) for application usage,
[`ARCHITECTURE.md`](ARCHITECTURE.md) for ownership and security boundaries, and
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the pinned toolchain and verification commands.
Maintainers should use [`MAINTAINING.md`](MAINTAINING.md) for checked-target refresh,
generation, evidence, rollback, and release review.

Small crate examples can be run from this directory:

```bash
cargo run -p dagger-sdk --example first-pipeline
```

The standalone application examples under [`examples`](examples) each have their own
manifest; their exact commands are listed in [`examples/README.md`](examples/README.md).
