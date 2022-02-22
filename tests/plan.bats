setup() {
    load 'helpers'

    common_setup
}

@test "plan/hello" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  "$DAGGER" "do" -p ./plan/hello-europa test
}

@test "plan/proxy invalid schema" {
  cd "$TESTDIR"
  run "$DAGGER" "do" -p ./plan/proxy/invalid_schema.cue verify
  assert_failure
}

@test "plan/proxy invalid value" {
  cd "$TESTDIR"
  run "$DAGGER" "do" -p ./plan/proxy/invalid_value.cue verify
  assert_failure
}

@test "plan/proxy incomplete unix" {
  cd "$TESTDIR"
  run "$DAGGER" "do" -p ./plan/proxy/incomplete_unix.cue verify
  assert_failure
}

@test "plan/proxy incomplete service" {
  cd "$TESTDIR"
  run "$DAGGER" "do" -p ./plan/proxy/incomplete_service.cue verify
  assert_output --partial 'mount "docker" is not concrete'
}

@test "plan/proxy unix" {
  cd "$TESTDIR"
  "$DAGGER" "do" -p ./plan/proxy/unix.cue verify
}

@test "plan/inputs/directories" {
  cd "$TESTDIR"
  "$DAGGER" "do" -p ./plan/inputs/directories/valid exists

  run "$DAGGER" "do" -p ./plan/inputs/directories/invalid notExists
	assert_failure
	assert_output --partial 'fasdfsdfs" does not exist'

  run "$DAGGER" "do" -p ./plan/inputs/directories/valid conflictingValues
	assert_failure
	assert_output --partial 'conflicting values "local directory" and "local dfsadf"'
}

@test "plan/inputs/secrets" {
  cd "$TESTDIR"
  "$DAGGER" "do" -p ./plan/inputs/secrets test valid
  "$DAGGER" "do" -p ./plan/inputs/secrets test relative

  run "$DAGGER" "do" -p ./plan/inputs/secrets test badCommand
	assert_failure
	assert_output --partial 'failed: exec: "rtyet": executable file not found'

  run "$DAGGER" "do" -p ./plan/inputs/secrets test badArgs
	assert_failure
	assert_output --partial 'option'
}

@test "plan/with" {
  cd "$TESTDIR"
  "$DAGGER" "do" --with 'inputs: params: foo:"bar"' -p ./plan/with test params
  "$DAGGER" "do" --with 'actions: test: direct: env: FOO: "bar"' -p ./plan/with test direct

  run "$DAGGER" "do" --with 'inputs: params: foo:1' -p ./plan/with test params
  assert_failure
  assert_output --partial "conflicting values string and 1"

  run "$DAGGER" "do" -p ./plan/with test params
  assert_failure
  assert_output --partial "actions.test.params.env.FOO: non-concrete value string"
}

@test "plan/platform" {
   cd "$TESTDIR"

   # Run with amd64 platform
   run "$DAGGER" up ./plan/platform/config_platform_linux_amd64.cue

   # Run with arm64 platform
   run "$DAGGER" up ./plan/platform/config_platform_linux_arm64.cue

   # Run with invalid platform
   run "$DAGGER" up ./plan/platform/config_platform_failure_invalid_platform.cue
   assert_failure
}
