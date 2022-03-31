common_setup() {
    load 'node_modules/bats-support/load'
    load 'node_modules/bats-assert/load'

    TESTDIR="$( cd "$( dirname "$BATS_TEST_FILENAME" )" >/dev/null 2>&1 && pwd )"

    DAGGER="${DAGGER_BINARY:-$TESTDIR/../cmd/dagger/dagger}"
    export DAGGER

    DAGGER_LOG_FORMAT="plain"
    export DAGGER_LOG_FORMAT

    DAGGER_TELEMETRY_DISABLE="1"
    export DAGGER_TELEMETRY_DISABLE

    export DAGGER_LOG_LEVEL="debug"
    if [ -n "$GITHUB_ACTIONS" ];
    then
        export DAGGER_CACHE_TO="type=gha,mode=max,scope=integration-tests-$BATS_TEST_NAME"
        export DAGGER_CACHE_FROM="type=gha,scope=integration-tests-$BATS_TEST_NAME"
    fi

    SOPS_AGE_KEY_FILE=~/.config/dagger/keys.txt
    export SOPS_AGE_KEY_FILE
}

# dagger helper to execute the right binary
dagger() {
    "${DAGGER}" "$@"
}
