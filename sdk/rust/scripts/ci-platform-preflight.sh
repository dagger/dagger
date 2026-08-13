#!/usr/bin/env bash

set -euo pipefail

rust_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$rust_root"

output="${1:?an observation output path is required}"
cargo build -p dagger-sdk-completeness --bin dagger-rust-sdk-platform --locked
./target/debug/dagger-rust-sdk-platform native --output "$output"
