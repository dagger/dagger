setup() {
    load 'helpers'

    common_setup
}

@test "plan: hello" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/hello-europa
  assert_success
}