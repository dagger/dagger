setup() {
    load 'helpers'

    common_setup
}

@test "plan/hello" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/hello-europa
}

@test "plan/context/services invalid schema" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/context/services/invalid_schema.cue
  assert_failure
}

@test "plan/context/services invalid value" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/context/services/invalid_value.cue
  assert_failure
}

@test "plan/context/services incomplete unix" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/context/services/incomplete_unix.cue
  assert_failure
}

@test "plan/context/services incomplete service" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/context/services/incomplete_service.cue
  assert_output --partial "pipeline was partially executed because of missing inputs"
}

@test "plan/context/services unix" {
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/context/services/unix.cue
}