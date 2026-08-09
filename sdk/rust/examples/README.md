# Dagger Rust SDK examples

These standalone workspaces consume the local `dagger-sdk` crate and exercise realistic
application pipelines. Run commands from `sdk/rust`; each program starts its own SDK
client and therefore also works under `dagger run` when an existing session is desired.

| Example | What it demonstrates | Command |
| --- | --- | --- |
| [CLI](cli/src/main.rs) | Build a Rust CLI in a container and export the binary | `cargo run --manifest-path examples/cli/Cargo.toml` |
| [Backend](backend/src/main.rs) | Build, package, and optionally publish an Axum service | `cargo run --manifest-path examples/backend/Cargo.toml -- --help` |
| [Frontend](frontend/src/main.rs) | Build and package a Leptos and Tailwind application | `cargo run --manifest-path examples/frontend/Cargo.toml -- --help` |

The smaller API-focused examples packaged inside `dagger-sdk` run with
`cargo run -p dagger-sdk --example first-pipeline` and related example names.
