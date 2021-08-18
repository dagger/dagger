common_setup() {
    load 'node_modules/bats-support/load'
    load 'node_modules/bats-assert/load'

    # Dagger Binary
    # FIXME: `command -v` must be wrapped in a sub-bash,
    #   otherwise infinite recursion when DAGGER_BINARY is not set.
    export DAGGER="${DAGGER_BINARY:-$(bash -c 'command -v dagger')}"

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
#
# To use testdata directory in tests, add the package name as second flag
# Usage:
# copy_to_sandbox myenv mypackage
copy_to_sandbox() {
    local name="$1"
    local source="$DAGGER_WORKSPACE"/.dagger/env/"$name"
    local target="$DAGGER_SANDBOX"/.dagger/env/"$name"

    cp -a "$source" "$target"

    if [ -d "$2" ]; then
      local package="$2"
      local source_package="$DAGGER_WORKSPACE"/"$package"
      local target_package="$DAGGER_SANDBOX"/

      cp -a "$source_package" "$target_package"
    fi
}
# Check if there is a local kubernetes cluster.
#
# This is need to do kubernetes test in the CI.
skip_unless_local_localstack() {
    if   [ "$(curl -s http://localhost:4566)" = '{"status": "running"}' ]; then
        echo "Localstack available"
    else
        skip "Localstack not available"
    fi
}

# Check if there is a local kubernetes cluster.
#
# This is need to do kubernetes test in the CI.
skip_unless_local_kube() {
    if [ -f ~/.kube/config ] && grep -q "user: kind-kind" ~/.kube/config &> /dev/null && grep -q "127.0.0.1" ~/.kube/config &> /dev/null; then
        echo "Kubernetes available"
    else
        skip "local kubernetes cluster not available"
    fi
}