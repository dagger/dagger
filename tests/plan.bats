setup() {
    load 'helpers'

    common_setup
}

@test "plan/hello" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/hello-europa
}

@test "plan/proxy invalid schema" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/proxy/invalid_schema.cue
  assert_failure
}

@test "plan/proxy invalid value" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/proxy/invalid_value.cue
  assert_failure
}

@test "plan/proxy incomplete unix" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/proxy/incomplete_unix.cue
  assert_failure
}

@test "plan/proxy incomplete service" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/proxy/incomplete_service.cue
  assert_output --partial "pipeline was partially executed because of missing inputs"
}

@test "plan/proxy unix" {
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/proxy/unix.cue
}

@test "plan/inputs/directories exists" {
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/inputs/directories/exists.cue
}

@test "plan/inputs/directories not exists" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/inputs/directories/not_exists.cue
	assert_failure
	assert_output --partial 'tests/fasdfsdfs" does not exist'
}

@test "plan/inputs/directories conflicting values" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/inputs/directories/conflicting_values.cue
	assert_failure
	assert_output --partial 'failed to up environment: actions.verify.contents: conflicting values "local directory" and "local dfsadf"'
}