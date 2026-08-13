#!/usr/bin/env bash

set -euo pipefail

rust_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$rust_root"

dependencies() {
  cargo metadata --no-deps --format-version 1 --locked >/dev/null
  cargo metadata --manifest-path examples/backend/Cargo.toml --no-deps --format-version 1 --locked >/dev/null
  cargo metadata --manifest-path examples/cli/Cargo.toml --no-deps --format-version 1 --locked >/dev/null
  cargo metadata --manifest-path examples/frontend/Cargo.toml --no-deps --format-version 1 --locked >/dev/null
  cargo deny check
  cargo metadata --no-deps --format-version 1 --locked |
    jq -e '
      ([.packages[] | select(.publish != []) | .name] | sort) == ["dagger-sdk", "dagger-sdk-macros"] and
      (.packages[] | select(.name == "dagger-sdk") |
        (.features == {"default": ["gen"], "gen": []}) and
        ([.dependencies[] | select(.name == "dagger-sdk-macros" and .req == "=1.0.0-beta.10" and .kind == null)] | length) == 1 and
        ([.dependencies[] | select(.req == "*")] | length == 0) and
        ([.dependencies[] | select(.source != null and (.source | startswith("registry+https://github.com/rust-lang/crates.io-index") | not))] | length == 0)
      ) and
      (.packages[] | select(.name == "dagger-sdk-macros") |
        (.edition == "2024") and
        (.rust_version == "1.97.1") and
        ([.dependencies[] | select(.name == "dagger-sdk")] | length) == 0
      )'
}

source_policy() {
  cargo test -p dagger-sdk-macros --locked
  cargo test -p dagger-sdk --test source_policy --all-features --locked
  cargo test -p dagger-sdk --test module_authoring_compile --locked
  cargo test -p dagger-bootstrap --test generation --locked
  cargo test -p dagger-codegen --test render --test projection --locked
  cargo test -p dagger-codegen --test engine_operations --locked
  cargo test -p dagger-codegen --test module_diagnostics --locked
  cargo test -p dagger-sdk-engine --test packaging_properties --locked
  cargo test -p dagger-sdk-engine --test project_properties --locked
  cargo test -p dagger-sdk-engine --test protocol_properties --locked
  cargo test -p dagger-sdk-engine --test checkpoint_properties --locked
  cargo test -p dagger-sdk-completeness --test engine_integration --locked
  cargo test -p dagger-sdk-completeness --test module_authoring_evidence --locked
  cargo test -p dagger-sdk-completeness --test signoff_security_properties --locked
  cargo test -p dagger-sdk-completeness --test signoff_artifact_properties --locked
  cargo test -p dagger-sdk-completeness --test artifact_scanner_fixture --locked
  cargo test -p dagger-sdk-completeness --test security_repository_policy --locked
  cargo run -p dagger-sdk-completeness --bin dagger-conformance-security-policy --locked -- --check
  cargo test -p dagger-sdk --all-features --locked \
    public_error_rendering_is_redacted_while_sources_remain_inspectable
  cargo test -p dagger-sdk --all-features --locked \
    session_control_and_process_input_debug_never_render_credentials
}

runtime() {
  (
    cd runtime
    DAGGER_SESSION_PORT=1 \
      DAGGER_SESSION_TOKEN=engine-free-static-check \
      go test ./...
  )
}

packages() {
  cargo package -p dagger-sdk-macros --locked
  local macro_files
  macro_files="$(cargo package -p dagger-sdk-macros --list --locked)"
  grep -Fqx -- 'README.md' <<<"$macro_files"
  grep -Fqx -- 'src/lib.rs' <<<"$macro_files"

  # The local patch lets Cargo verify both halves before their first ordered
  # release; the packaged SDK manifest still contains the exact registry edge.
  cargo package -p dagger-sdk --all-features --locked \
    --config 'patch.crates-io.dagger-sdk-macros.path="crates/dagger-sdk-macros"'
  local package_files
  package_files="$(cargo package -p dagger-sdk --list --locked)"
  grep -Fqx -- 'README.md' <<<"$package_files"
  grep -Fqx -- 'LICENSE' <<<"$package_files"
  grep -Fqx -- 'examples/first-pipeline/main.rs' <<<"$package_files"
  grep -Fqx -- 'src/gen/mod.rs' <<<"$package_files"
}

case "${1:-all}" in
  dependencies)
    dependencies
    ;;
  source-policy)
    source_policy
    ;;
  runtime)
    runtime
    ;;
  packages)
    packages
    ;;
  all)
    dependencies
    source_policy
    runtime
    packages
    ;;
  *)
    printf 'usage: %s [dependencies|source-policy|runtime|packages|all]\n' "$0" >&2
    exit 64
    ;;
esac
