setup() {
    load 'helpers'

    common_setup
}

@test "example: react" {
    cp -R "$TESTDIR"/examples/react/.dagger "$DAGGER_WORKSPACE"/.dagger
    cp -R "$TESTDIR"/../examples/react/*.cue "$DAGGER_WORKSPACE"/.dagger/env/default/plan

    "$DAGGER" up

    # curl the URL we just deployed to check if it worked
    deployUrl=$("$DAGGER" query -l error -f text www.deployUrl)
    run curl -sS "$deployUrl"
    assert_success
    assert_output --partial "Todo App"
}