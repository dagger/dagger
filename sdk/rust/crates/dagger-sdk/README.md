# Dagger Rust SDK

The Rust SDK provides an owned, asynchronous client for the Dagger GraphQL API. Its
default connector can reuse a session created by `dagger run` or provision and launch
the exact Dagger CLI release compiled into the SDK.

The client shares one connection and one close result across generated handles, raw
queries, compositional queries, and clones. Implicit HTTP is authenticated and
loopback-only; downloaded CLI bytes are checksum-verified before use.

## Install

```bash
cargo add dagger-sdk
```

The SDK uses Tokio for asynchronous work. Applications need a Tokio runtime, for
example:

```bash
cargo add tokio --features macros,rt-multi-thread
```

## Use

```rust,no_run
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = dagger_sdk::connect().await?;

    let output = client
        .query()
        .container()
        .from("alpine:3.22")
        .with_exec(vec!["echo", "hello from Dagger"])
        .stdout()
        .await?;

    println!("{}", output.trim());
    client.close().await?;
    Ok(())
}
```

Run the program normally and the SDK will establish an implicit Dagger session:

```bash
cargo run
```

Run it through Dagger to reuse an existing session:

```bash
dagger run cargo run
```

See the crate documentation for raw GraphQL, typed execution errors, diagnostics,
trace propagation, compatibility policy, injected connections, and shutdown details.

## Examples and contributing

Workspace examples live in [`../../examples`](../../examples). From `sdk/rust`, run
one with `cargo run --example first-pipeline`.

Development and verification commands are documented in
[`../../CONTRIBUTING.md`](../../CONTRIBUTING.md).
