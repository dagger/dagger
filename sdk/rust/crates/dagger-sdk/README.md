# Dagger Rust SDK

The Rust SDK provides an owned, asynchronous client for the Dagger GraphQL API. Its
default connector can reuse a session created by `dagger run` or provision and launch
the exact Dagger CLI release compiled into the SDK.

The client shares one connection and one close result across generated handles, raw
queries, compositional queries, and clones. Implicit HTTP is authenticated and
loopback-only; downloaded CLI bytes are checksum-verified before use.

## Install

The SDK is supplied as two checked repository release artifacts rather than through
crates.io. Download `dagger-sdk-1.0.0-beta.11.rust.1.crate`,
`dagger-sdk-macros-1.0.0-beta.11.rust.1.crate`, and `SHA256SUMS` from the same manual
GitHub Release, then verify both package checksums before unpacking them:

```console
mkdir -p vendor/dagger-sdk vendor/dagger-sdk-macros
tar -xzf dagger-sdk-1.0.0-beta.11.rust.1.crate -C vendor/dagger-sdk --strip-components=1
tar -xzf dagger-sdk-macros-1.0.0-beta.11.rust.1.crate -C vendor/dagger-sdk-macros --strip-components=1
```

Reference the unpacked SDK and its exact procedural-macro companion from
`Cargo.toml`:

```toml
[dependencies]
dagger-sdk = { path = "vendor/dagger-sdk" }
tokio = { version = "1.35.1", features = ["macros", "rt-multi-thread"] }

[patch.crates-io]
dagger-sdk-macros = { path = "vendor/dagger-sdk-macros" }
```

The patch is required because the packaged SDK pins its macro companion to the exact
matching version. Keep both directories together and commit an application lockfile
under the application's own reproducibility policy.

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

Module authors use the `object`, `interface`, `enum_type`, `scalar`, and `methods`
attributes re-exported from this crate. Their exact-version procedural implementation
lives in `dagger-sdk-macros`; applications should not add that companion directly.
The authoring syntax, state/dispatch model, and current verification boundary are
documented in [`../../MODULE_AUTHORING.md`](../../MODULE_AUTHORING.md).

Generated standalone clients also reuse this crate's owned lifecycle, Core bindings,
transport, errors, and IDs by identity. Their `dagger_client` namespace adds one bound
module without copying Core or merging dependency modules. See
[`../../CLIENT_GENERATION.md`](../../CLIENT_GENERATION.md).

## Features

| Cargo selection | Public surface | Intended use |
| --- | --- | --- |
| default | handwritten client plus generated core-schema bindings | normal SDK applications |
| `--features gen` | same generated surface, when defaults were disabled explicitly | selective workspace configurations |
| `--no-default-features` | owned client, raw GraphQL, diagnostics, errors, and scalar values | raw queries or smaller integrations that do not need typed core bindings |
| `--all-features` | the complete supported SDK surface | CI and release verification |

The generated types are re-exported at the crate root only when `gen` is enabled.
Disabling it never changes the raw request/response or client lifecycle contracts.

## Examples and contributing

Small examples are packaged with the crate. From `sdk/rust`, run one with
`cargo run -p dagger-sdk --example first-pipeline`. Standalone application examples
live in [`../../examples`](../../examples).

Development and verification commands are documented in
[`../../CONTRIBUTING.md`](../../CONTRIBUTING.md); generated-client maintenance and
release assembly are documented in [`../../MAINTAINING.md`](../../MAINTAINING.md).
