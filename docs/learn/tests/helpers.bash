## Doc commands are being extracted from this file and helpers.
## Indentation is important, please append at the end

common_setup() {
    load 'node_modules/bats-support/load'
    load 'node_modules/bats-assert/load'

    # Dagger Binary
    # FIXME: `command -v` must be wrapped in a sub-bash,
    #   otherwise infinite recursion when DAGGER_BINARY is not set.
    export DAGGER="${DAGGER_BINARY:-$(bash -c 'command -v dagger')}"

    # Set the project to the universe directory (so tests can run from anywhere)
    UNIVERSE="$( cd "$( dirname "$BATS_TEST_FILENAME" )" >/dev/null 2>&1 && pwd )"
    DAGGER_PROJECT="$UNIVERSE"
    export DAGGER_PROJECT

    # Force plain printing for error reporting
    DAGGER_LOG_FORMAT="plain"
    export DAGGER_LOG_FORMAT

    # Sandbox project.
    DAGGER_SANDBOX="$(mktemp -d -t dagger-project-XXXXXX)"
    export DAGGER_SANDBOX
    dagger init --project "$DAGGER_SANDBOX"

    # allows the use of `sops`
    SOPS_AGE_KEY_FILE=~/.config/dagger/keys.txt
    export SOPS_AGE_KEY_FILE
}

# dagger helper to execute the right binary
dagger() {
    "${DAGGER}" "$@"
}

# Setup sandbox for dagger example
# It clones the example repository and update Sandbox to
setup_example_sandbox() {
  git -C "$DAGGER_SANDBOX" clone https://github.com/dagger/examples

  export DAGGER_SANDBOX="$DAGGER_SANDBOX"/examples/todoapp
  dagger --project "$DAGGER_SANDBOX" init
}


# copy an environment from the current project to the sandbox.
#
# this is needed if the test requires altering inputs without dirtying the
# current environment.
# Usage:
# copy_to_sandbox myenv
# dagger input secret -w "$DAGGER_SANDBOX" -e myenv "temporary change"
# dagger up --project "$DAGGER_SANDBOX" -e myenv
#
# To use testdata directory in tests, add the package name as second flag
# Usage:
# copy_to_sandbox myenv mypackage
copy_to_sandbox() {
    local name="$1"
    local source="$DAGGER_PROJECT"/.dagger/env/"$name"
    local target="$DAGGER_SANDBOX"/.dagger/env/"$name"

    cp -a "$source" "$target"

    if [ -d "$2" ]; then
      local package="$2"
      local source_package="$DAGGER_PROJECT"/"$package"
      local target_package="$DAGGER_SANDBOX"/

      cp -a "$source_package" "$target_package"
    fi
}

# Check if there is a localstack instance.
#
# This is needed to do docs test in the CI.
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
    if [ -f ~/.kube/config ] && grep -q "127.0.0.1" ~/.kube/config; then
        echo "Kubernetes available"
    else
        skip "local kubernetes cluster not available"
    fi
}
