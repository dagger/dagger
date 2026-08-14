# Dagger Rust SDK examples

These standalone workspaces consume the local `dagger-sdk` crate and exercise realistic
application pipelines. Run commands from `sdk/rust`; each program starts its own SDK
client and therefore also works under `dagger run` when an existing session is desired.

| Example | What it demonstrates | Command |
| --- | --- | --- |
| [CLI](cli/src/main.rs) | Build a Rust CLI in a container and export the binary | `cargo run --locked --manifest-path examples/cli/Cargo.toml` |
| [Backend](backend/src/main.rs) | Build and evaluate an Axum service image | `cargo run --locked --manifest-path examples/backend/Cargo.toml` |
| [Frontend](frontend/src/main.rs) | Build and evaluate a Leptos web image | `cargo run --locked --manifest-path examples/frontend/Cargo.toml` |

The smaller API-focused examples packaged inside `dagger-sdk` run with
`cargo run -p dagger-sdk --example first-pipeline` and related example names.

The backend and frontend examples are build-only by default and make no registry write.
Publishing is deliberately separate: select the `publish` subcommand, provide the complete
`--address`, and add `--allow-publish` to confirm the external write. Exact-target SDK sign-off
uses only the default build path and rejects any publication attempt.

Exact-target sign-off additionally selects a hidden, fixed-path build export for the backend and
frontend images. It forces OCI media types with Gzip-compressed layers, retains each tar only
inside the isolated case workspace, rejects output larger than 256 MiB, and never turns that
inspection path into a registry write.
