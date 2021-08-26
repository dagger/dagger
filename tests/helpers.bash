common_setup() {
    load 'node_modules/bats-support/load'
    load 'node_modules/bats-assert/load'

    TESTDIR="$( cd "$( dirname "$BATS_TEST_FILENAME" )" >/dev/null 2>&1 && pwd )"

    DAGGER="${DAGGER_BINARY:-$TESTDIR/../cmd/dagger/dagger}"
    export DAGGER

    DAGGER_LOG_FORMAT="pretty"
    export DAGGER_LOG_FORMAT

    DAGGER_WORKSPACE="$(mktemp -d -t dagger-workspace-XXXXXX)"
    export DAGGER_WORKSPACE

    SOPS_AGE_KEY_FILE=~/.config/dagger/keys.txt
    export SOPS_AGE_KEY_FILE
}

dagger_new_with_plan() {
    local name="$1"
    local sourcePlan="$2"

    cp -a "$sourcePlan"/* "$DAGGER_WORKSPACE"

    "$DAGGER" new "$name"
}

dagger_new_with_env() {
    local sourcePlan="$1"
    
    "$DAGGER" init -w "$DAGGER_WORKSPACE"
    rsync -av "$sourcePlan"/ "$DAGGER_WORKSPACE"
}

# dagger helper to execute the right binary
dagger() {
    "${DAGGER}" "$@"
}

skip_unless_secrets_available() {
    local inputFile="$1"
    sops exec-file "$inputFile" echo  > /dev/null 2>&1 || skip "$inputFile cannot be decrypted"
}

skip_unless_local_kube() {
    if [ -f ~/.kube/config ] && grep -q "user: kind-kind" ~/.kube/config &> /dev/null && grep -q "127.0.0.1" ~/.kube/config &> /dev/null; then
        echo "Kubernetes available"
    else
        skip "local kubernetes cluster not available"
    fi
}
