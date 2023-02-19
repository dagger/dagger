# dagger-sdk

A dagger sdk written in rust for rust.

## Examples

See [examples](./examples/)

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
    let client = dagger_sdk::connect()?;

    let version = client
        .container()
        .from("golang:1.19")
        .with_exec(vec!["go", "version"])
        .stdout().await?;

    println!("Hello from Dagger and {}", version.trim());

    Ok(())
}
```

And run it like a normal application:

```bash
cargo run
```

### Disclaimer

You are free to use something else than `tokio`, I haven't tested it with
anything else, but it should work with any other runtime. We don't rely on it
specifically. That might change in the future though.
