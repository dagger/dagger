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

@test "plan/inputs/directories relative directories" {
  cd "$TESTDIR"
  cd "$TESTDIR"/plan/inputs

  "$DAGGER" --europa up ./directories/exists.cue
}

@test "plan/inputs/directories not exists" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/inputs/directories/not_exists.cue
	assert_failure
	assert_output --partial 'fasdfsdfs" does not exist'
}

@test "plan/inputs/directories conflicting values" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/inputs/directories/conflicting_values.cue
	assert_failure
	assert_output --partial 'conflicting values "local directory" and "local dfsadf"'
}

@test "plan/inputs/secrets exec" {
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/inputs/secrets/exec.cue
}

@test "plan/inputs/secrets exec relative" {
  cd "$TESTDIR"
  "$DAGGER" --europa up ./plan/inputs/secrets/exec.cue
}

@test "plan/inputs/secrets invalid command" {
  cd "$TESTDIR"
  run "$DAGGER" --europa up ./plan/inputs/secrets/invalid_command.cue
	assert_failure
	assert_output --partial 'failed: exec: "rtyet": executable file not found'
}

@test "plan/params" {
  cd "$TESTDIR"
  "$DAGGER" --europa up --with 'inputs: params: foo:"bar"' ./plan/inputs/params/main.cue
  
  run "$DAGGER" --europa up --with 'inputs: params: foo:1' ./plan/inputs/params/main.cue
  assert_failure
  assert_output --partial "conflicting values string and 1"
  
  run "$DAGGER" --europa up ./plan/inputs/params/main.cue
  assert_failure
  assert_output --partial "actions.verify.env.FOO: non-concrete value string"
}

@test "plan/outputs" {
    cd "$TESTDIR"/plan/outputs

    rm -f "./out/test"
    "$DAGGER" --europa up ./outputs.cue
    assert [ -f "./out/test" ]
}

@test "plan/outputs relative paths" {
    cd "$TESTDIR"/plan

    rm -f "./outputs/out/test"
    "$DAGGER" --europa up ./outputs/outputs.cue
    assert [ -f "./outputs/out/test" ]
}

@test "plan/platform" {
   cd "$TESTDIR"

   # Run with amd64 platform
   run "$DAGGER" --europa up ./plan/platform/config_platform_linux_amd64.cue

   # Run with arm64 platform
   run "$DAGGER" --europa up ./plan/platform/config_platform_linux_arm64.cue

   # Run with invalid platform
   run "$DAGGER" --europa up ./plan/platform/config_platform_failure_invalid_platform.cue
   assert_failure
}