setup() {
    load 'helpers'

    common_setup
}

@test "example: react" {
    DAGGER_WORKSPACE="$TESTDIR"/../examples/react
    export DAGGER_WORKSPACE

    "$DAGGER" up

    # curl the URL we just deployed to check if it worked
    deployUrl=$("$DAGGER" query -l error -f text www.deployUrl)
    run curl -sS "$deployUrl"
    assert_success
    assert_output --partial "Todo App"
}