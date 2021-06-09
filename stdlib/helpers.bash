common_setup() {
    load 'node_modules/bats-support/load'
    load 'node_modules/bats-assert/load'

    # Dagger Binary
    DAGGER="${DAGGER_BINARY:-$(command -v dagger)}"
    export DAGGER

    # Set the workspace to the universe directory (so tests can run from anywhere)
    UNIVERSE="$( cd "$( dirname "$BATS_TEST_FILENAME" )" >/dev/null 2>&1 && pwd )"
    DAGGER_WORKSPACE="$UNIVERSE"
    export DAGGER_WORKSPACE

    # Force pretty printing for error reporting
    DAGGER_LOG_FORMAT="pretty"
    export DAGGER_LOG_FORMAT

    # Sandbox workspace.
    DAGGER_SANDBOX="$(mktemp -d -t dagger-workspace-XXXXXX)"
    export DAGGER_SANDBOX
    dagger init -w "$DAGGER_SANDBOX"

    # allows the use of `sops`
    SOPS_AGE_KEY_FILE=~/.config/dagger/keys.txt
    export SOPS_AGE_KEY_FILE
}

# dagger helper to execute the right binary
dagger() {
    "${DAGGER}" "$@"
}

# copy an environment from the current workspace to the sandbox.
#
# this is needed if the test requires altering inputs without dirtying the
# current environment.
# Usage:
# copy_to_sandbox myenv
# dagger input secret -w "$DAGGER_SANDBOX" -e myenv "temporary change"
# dagger up -w "$DAGGER_SANDBOX" -e myenv
copy_to_sandbox() {
    local name="$1"
    local source="$DAGGER_WORKSPACE"/.dagger/env/"$name"
    local target="$DAGGER_SANDBOX"/.dagger/env/"$name"

    cp -a "$source" "$target"
}