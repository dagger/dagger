setup() {
    load 'helpers'

    common_setup
}

@test "example: react" {
    skip_unless_secrets_available "$TESTDIR"/examples/react/inputs.yaml

    "$DAGGER" new --plan-dir "$TESTDIR"/../examples/react react
    sops -d "$TESTDIR"/examples/react/inputs.yaml | "$DAGGER" -d "react" input yaml "" -f -
    "$DAGGER" up -d "react"

    # curl the URL we just deployed to check if it worked
    deployUrl=$("$DAGGER" query -l error -f text -d "react" www.deployUrl)
    echo "=>$deployUrl<="
    run curl -sS "$deployUrl"
    assert_success
    assert_output --partial "Todo App"
}
