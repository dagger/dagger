common_setup() {
    load "$(dirname "${BASH_SOURCE[0]}")/node_modules/bats-support/load.bash"
    load "$(dirname "${BASH_SOURCE[0]}")/node_modules/bats-assert/load.bash"

    # Dagger & CUE Binaries
    # FIXME: `command -v` must be wrapped in a sub-bash,
    #   otherwise infinite recursion when DAGGER_BINARY/CUE_BINARY is not set.
    export DAGGER="${DAGGER_BINARY:-$(bash -c 'command -v dagger')}"
    export CUE="${CUE_BINARY:-$(bash -c 'command -v cue')}"

    # Disable telemetry
    DAGGER_TELEMETRY_DISABLE="1"
    export DAGGER_TELEMETRY_DISABLE

    # Force plain printing for error reporting
    DAGGER_LOG_FORMAT="plain"
    export DAGGER_LOG_FORMAT

    export DAGGER_LOG_LEVEL="debug"
    if [ -n "$GITHUB_ACTIONS" ];
    then
        export DAGGER_CACHE_TO="$DAGGER_CACHE_TO-$BATS_TEST_NAME"
        export DAGGER_CACHE_FROM="$DAGGER_CACHE_FROM-$BATS_TEST_NAME"
    fi

    # cd into the directory containing the bats file
    cd "$BATS_TEST_DIRNAME" || exit 1
}

# cue helper to execute the right binary
cue() {
    "${CUE}" "$@"
}

# dagger helper to execute the right binary
dagger() {
    "${DAGGER}" "$@"
}
