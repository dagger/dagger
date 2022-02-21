setup() {
    load 'helpers'

    common_setup
}

@test "plan/hello" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  "$DAGGER" up ./plan/hello-europa
}

@test "plan/proxy invalid schema" {
  cd "$TESTDIR"
  run "$DAGGER" up ./plan/proxy/invalid_schema.cue
  assert_failure
}

# TODO/FIXME: this test is broken, it's not asserting that the proxy is not running
# @test "plan/proxy invalid value" {
#   cd "$TESTDIR"
#   run "$DAGGER" up ./plan/proxy/invalid_value.cue
#   assert_failure
# }

@test "plan/proxy incomplete unix" {
  cd "$TESTDIR"
  run "$DAGGER" up ./plan/proxy/incomplete_unix.cue
  assert_failure
}

# TODO/FIXME: this test is broken, it's not asserting that the proxy is not running
# @test "plan/proxy incomplete service" {
#   cd "$TESTDIR"
#   run "$DAGGER" up ./plan/proxy/incomplete_service.cue
#   assert_output --partial "pipeline was partially executed because of missing inputs"
# }

@test "plan/proxy unix" {
  cd "$TESTDIR"
  "$DAGGER" up ./plan/proxy/unix.cue
}

@test "plan/inputs/directories exists" {
  cd "$TESTDIR"
  "$DAGGER" up ./plan/inputs/directories/exists.cue
}

@test "plan/inputs/directories relative directories" {
  cd "$TESTDIR"
  cd "$TESTDIR"/plan/inputs

  "$DAGGER" up ./directories/exists.cue
}

@test "plan/inputs/directories not exists" {
  cd "$TESTDIR"
  run "$DAGGER" up ./plan/inputs/directories/not_exists.cue
	assert_failure
	assert_output --partial 'fasdfsdfs" does not exist'
}

@test "plan/inputs/directories conflicting values" {
  cd "$TESTDIR"
  run "$DAGGER" up ./plan/inputs/directories/conflicting_values.cue
	assert_failure
	assert_output --partial 'conflicting values "local directory" and "local dfsadf"'
}

@test "plan/inputs/secrets" {
  cd "$TESTDIR"
  "$DAGGER" up ./plan/inputs/secrets/exec.cue
  "$DAGGER" up ./plan/inputs/secrets/exec_relative.cue

  run "$DAGGER" up ./plan/inputs/secrets/invalid_command.cue
	assert_failure
	assert_output --partial 'failed: exec: "rtyet": executable file not found'

  run "$DAGGER" up ./plan/inputs/secrets/invalid_command_options.cue
	assert_failure
	assert_output --partial 'option'
}

@test "plan/with" {
  cd "$TESTDIR"
  "$DAGGER" up --with 'inputs: params: foo:"bar"' ./plan/with/params.cue
  "$DAGGER" up --with 'actions: verify: env: FOO: "bar"' ./plan/with/actions.cue

  run "$DAGGER" up --with 'inputs: params: foo:1' ./plan/with/params.cue
  assert_failure
  assert_output --partial "conflicting values string and 1"

  run "$DAGGER" up ./plan/with/params.cue
  assert_failure
  assert_output --partial "actions.verify.env.FOO: non-concrete value string"
}

@test "plan/outputs/directories" {
    cd "$TESTDIR"/plan/outputs/directories

    rm -f "./out/test"
    "$DAGGER" up ./outputs.cue
    assert [ -f "./out/test" ]
}

@test "plan/outputs/directories relative paths" {
    cd "$TESTDIR"/plan

    rm -f "./outputs/directories/out/test"
    "$DAGGER" up ./outputs/directories/outputs.cue
    assert [ -f "./outputs/directories/out/test" ]
}

@test "plan/outputs/files normal usage" {
  cd "$TESTDIR"/plan/outputs/files

  "$DAGGER" up ./usage.cue

  run ./test.sh
  assert_output "Hello World!"

  run ls -l "./test.sh"
  assert_output --partial "-rwxr-x---"

  rm -f "./test.sh"
}

@test "plan/outputs/files relative path" {
    cd "$TESTDIR"/plan

    "$DAGGER" up ./outputs/files/usage.cue
    assert [ -f "./outputs/files/test.sh" ]

    rm -f "./outputs/files/test.sh"
}

@test "plan/outputs/files default permissions" {
  cd "$TESTDIR"/plan/outputs/files

  "$DAGGER" up ./default_permissions.cue

  run ls -l "./test"
  assert_output --partial "-rw-r--r--"

  rm -f "./test"
}

@test "plan/outputs/files no contents" {
  cd "$TESTDIR"/plan/outputs/files

  run "$DAGGER" up ./no_contents.cue
  assert_failure
  assert_output --partial "contents is not set"

  assert [ ! -f "./test" ]
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
