setup() {
    load 'helpers'

    common_setup
}

@test "example: react" {
    skip_unless_secrets_available "$TESTDIR"/examples/react/inputs.yaml

    "$DAGGER" new --plan-dir "$TESTDIR"/../examples/react react
    sops -d "$TESTDIR"/examples/react/inputs.yaml | "$DAGGER" -e "react" input yaml "" -f -
    "$DAGGER" up -e "react"

    # curl the URL we just deployed to check if it worked
    deployUrl=$("$DAGGER" query -l error -f text -e "react" www.deployUrl)
    echo "=>$deployUrl<="
    run curl -sS "$deployUrl"
    assert_success
    assert_output --partial "Todo App"
}

@test "example: docker" {
     skip_unless_file_exist "$TESTDIR"/examples/docker/Dockerfile

    "$DAGGER" new --plan-dir "$TESTDIR"/../examples/docker/ docker
    "$DAGGER" input dir source "$TESTDIR"/examples/docker/ -e "docker"
    "$DAGGER" up -e "docker"
}