setup() {
    load 'helpers'

    common_setup
}

@test "plan: hello" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/hello-europa
}

@test "plan: unix socket" {
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/hello-europa
  run curl http://localhost:8080
  assert_output --partial "<title>Hello World</title>"
}