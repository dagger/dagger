# dagger-rs

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
cargo install dagger-sdk
```

### Usage

```rust
fn main() -> eyre::Result<()> {
    let client = dagger_sdk::client::connect()?;

    let version = client
        .container(None)
        .from("golang:1.19".into())
        .with_exec(vec!["go".into(), "version".into()], None)
        .stdout();

    println!("Hello from Dagger and {}", version.trim());

    Ok(())
}
```

And run it like a normal application:

```bash
cargo run
```
