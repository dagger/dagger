# dagger-bootstrap

`dagger-bootstrap` is the internal command-line entry point for generating the
[Dagger Rust SDK](https://github.com/dagger/dagger/tree/main/sdk/rust) client from
a Dagger engine GraphQL introspection response.

It connects two workspace components:

- `dagger-sdk` supplies the introspection response types.
- `dagger-codegen` translates the schema into the generated Rust client.

This crate is development and release tooling for the Rust SDK. Application authors
should depend on
[`dagger-sdk`](https://crates.io/crates/dagger-sdk) instead.

## Usage

The supported repository workflow is to regenerate all derived content from the
Dagger repository root:

```console
dagger generate -y
```

Rust SDK contributors can invoke the bootstrapper directly when working with an
existing introspection response:

```console
cargo run -p dagger-bootstrap -- generate /path/to/introspection.json \
  --output crates/dagger-sdk/src/gen.rs
```

Omit `--output` to write the generated client to standard output.

Generated client code must not be edited directly. Change `dagger-codegen` or its
templates, regenerate, and review the resulting diff.
