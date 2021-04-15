setup() {
    load 'helpers'

    common_setup
}

@test "example: react" {
  skip_unless_secrets_available "$TESTDIR"/examples/react/inputs.yaml

  run "$DAGGER" compute -l fatal "$TESTDIR"/../examples/react --input-yaml "$TESTDIR"/examples/react/inputs.yaml
  assert_success
  url=$(echo "$output" | jq -r .www.deployUrl)
  run curl -sS "$url"
  assert_success
  assert_output --partial "Todo App"
}
