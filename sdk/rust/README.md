> **Warning** This SDK is **experimental**. Please do not use it for anything
> mission-critical. Possible issues include:

- Missing features
- Stability issues
- Performance issues
- Lack of polish
- Upcoming breaking changes
- Incomplete or out-of-date documentation

Please report any issues you encounter. We appreciate and encourage
contributions. If you are a Rust developer interested in contributing to this
SDK, we welcome you!

# Dagger Rust SDK

## Plan for next release

- [x] Introduce [thiserror](https://docs.rs/thiserror/latest/thiserror/) for
      better errors
- [x] Add compatibility with `dagger run`
- [ ] Add open telemetry tracing to the sdk
- [ ] Remove `id().await?` from passing to other dagger graphs, this should make
      the design much cleaner
- [x] Update to newest upstream release
- [ ] Fix bugs
  - [x] Run in conjunction with golang and other sdks
  - [ ] Stabilize the initial `Arc<Query>` model into something more extensible

## Examples

See [examples](./crates/dagger-sdk/examples/)

Run them like so

```bash
cargo run --example first-pipeline
```

The examples match the folder name in each directory in examples

## Install

Simply install like:

```bash
cargo add dagger-sdk
```

### Usage

```rust
#[tokio::main]
async fn main() -> eyre::Result<()> {
    let client = dagger_sdk::connect().await?;

    let version = client
        .container()
        .from("golang:1.19")
        .with_exec(vec!["go", "version"])
        .stdout()
        .await?;

    println!("Hello from Dagger and {}", version.trim());

    Ok(())
}
```

And run it like a normal application:

```bash
cargo run
```

### Contributing

See [CONTRIBUTING](./CONTRIBUTING.md)

or just cargo make codegen
