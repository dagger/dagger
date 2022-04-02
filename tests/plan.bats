setup() {
    load 'helpers'

    common_setup
}

@test "plan/do: action sanity checks" {
  run "$DAGGER" "do" -p ./plan/do/actions.cue not exist
  assert_failure
  assert_output --partial "not found"
}

@test "plan/do: dynamic tasks - fails to find tasks" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  run "$DAGGER" "do" -p ./plan/do/dynamic_tasks.cue test b
  # No output because of weirdness with dynamic tasks, which causes it to fail
  refute_output --partial "actions.test.b"
}

@test "plan/do: dynamic tasks - fails to run tasks" {
  run "$DAGGER" "do" -p ./plan/do/dynamic_tasks.cue "test"
  refute_output --partial 'actions.test.b.y'
}

@test "plan/do: don't run unspecified tasks" {
  run "$DAGGER" "do" -p ./plan/do/do_not_run_unspecified_tasks.cue test
  assert_output --partial "actions.test.one.script"
  assert_output --partial "actions.test.three.script"
  assert_output --partial "actions.test.two.script"
  assert_output --partial "actions.image"
  assert_output --partial "actions.test.one"
  assert_output --partial "actions.test.two"
  assert_output --partial "actions.test.three"
  assert_output --partial "actions.test.one.export"
  assert_output --partial 'client.filesystem."./test_do".write'
  refute_output --partial "actions.notMe"
  refute_output --partial 'client.filesystem."./dependent_do".write'
  rm -f ./test_do
}

@test "plan/do: nice error message for 0.1.0 projects" {
  run "$DAGGER" "do" -p ./plan/do/error_message_for_0.1_projects.cue
  assert_output --partial "attempting to load a dagger 0.1.0 project."

  run "$DAGGER" "do" -p ./plan/do/error_message_for_0.1_projects.cue test
  assert_output --partial "attempting to load a dagger 0.1.0 project."
}

@test "plan/do: missing packages suggests project update" {
  run "$DAGGER" "do" -p ./plan/do/missing_dependencies.cue test
  assert_output --partial ": running \`dagger project update\` may resolve this"
}

@test "plan/hello" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  "$DAGGER" "do" -p ./plan/hello-europa test
}

@test "plan/client/filesystem/read/fs/usage" {
  cd "$TESTDIR/plan/client/filesystem/read/fs"

  "$DAGGER" "do" -p ./usage test valid

  run "$DAGGER" "do" -p ./usage test conflictingValues
  assert_failure
  assert_output --partial 'conflicting values "local directory" and "local foobar"'

  run "$DAGGER" "do" -p ./usage test excluded
  assert_failure
  assert_line --partial 'test.log: no such file or directory'

  run "$DAGGER" "do" -p ./usage test notExists
  assert_failure
  assert_output --partial 'test.json: no such file or directory'
}

@test "plan/client/filesystem/read/fs/not_exists" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/not_exists"

  run "$DAGGER" "do" -p . test
  assert_failure
  assert_output --partial 'path "/foobar" does not exist'
}


@test "plan/client/filesystem/read/fs/invalid_fs_input" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/invalid_fs_input"

  run "$DAGGER" "do" -p . test
  assert_failure
  assert_output --partial 'test.txt" is not a directory'
}


@test "plan/client/filesystem/read/fs/invalid_fs_type" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/invalid_fs_type"

  run "$DAGGER" "do" -p . test
  assert_failure
  assert_output --partial 'rootfs" cannot be a directory'
}

@test "plan/client/filesystem/read/fs/relative" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/relative"

  "$DAGGER" "do" -p . test valid

  run "$DAGGER" "do" -p . test notIncluded
  assert_failure
  assert_output --partial 'test.log: no such file or directory'
}

@test "plan/client/filesystem/read/file" {
  cd "$TESTDIR/plan/client/filesystem/read/file"

  "$DAGGER" "do" -p . test usage

  run "$DAGGER" "do" -p . test concrete
  assert_failure
  assert_output --partial "unexpected concrete value"
}

@test "plan/client/filesystem/write fs" {
  cd "$TESTDIR/plan/client/filesystem/write"

  rm -rf "./out_fs"

  "$DAGGER" "do" -p . test fs
  assert [ "$(cat ./out_fs/test)" = "foobar" ]

  rm -rf "./out_fs"
}

@test "plan/client/filesystem/write files" {
  cd "$TESTDIR/plan/client/filesystem/write"

  mkdir -p ./out_files
  rm -f ./out_files/*

  # -- string --

  "$DAGGER" "do" -p ./ test file

  assert [ "$(cat ./out_files/test.txt)" = "foobaz" ]
  run ls -l "./out_files/test.txt"
  assert_output --partial "-rw-r--r--"

  # -- secret --

  "$DAGGER" "do" -p ./ test secret

  assert [ "$(cat ./out_files/secret.txt)" = "foo-barab-oof" ]
  run ls -l "./out_files/secret.txt"
  assert_output --partial "-rw-------"

  rm -rf ./out_files
}

@test "plan/client/filesystem/conflict" {
  cd "$TESTDIR/plan/client/filesystem/conflict"

  echo -n foo > test.txt
  run "$DAGGER" "do" -p . test
  assert_line --regexp "client\.filesystem\..+\.write.+dependency=client\.filesystem\..+\.read"

  rm -f test.txt
}

@test "plan/client/network" {
  cd "$TESTDIR"
  "$DAGGER" "do" -p ./plan/client/network/valid.cue test

  run "$DAGGER" "do" -p ./plan/client/network/invalid.cue test
  assert_failure
}

@test "plan/client/env usage" {
  cd "${TESTDIR}"

  export TEST_STRING="foo"
  export TEST_SECRET="bar"

  "$DAGGER" "do" -p ./plan/client/env/usage.cue test
}

@test "plan/client/env not exists" {
  cd "${TESTDIR}"

  run "$DAGGER" "do" -p ./plan/client/env/usage.cue test
  assert_failure
  assert_output --regexp "environment variable \"TEST_(STRING|SECRET)\" not set"
}

@test "plan/client/env concrete" {
  cd "${TESTDIR}"

  export TEST_FAIL="foobar"

  run "$DAGGER" "do" -p ./plan/client/env/concrete.cue test
  assert_failure
  assert_output --partial "TEST_FAIL: unexpected concrete value"
}

@test "plan/client/commands" {
  cd "${TESTDIR}/plan/client/commands"

  "$DAGGER" "do" -p . test valid

  run "$DAGGER" "do" -p . test invalid
  assert_failure
  assert_output --partial 'exec: "foobar": executable file not found'
}

@test "plan/with" {
  cd "$TESTDIR"
  "$DAGGER" "do" --with 'actions: params: foo:"bar"' -p ./plan/with test params
  "$DAGGER" "do" --with 'actions: test: direct: env: FOO: "bar"' -p ./plan/with test direct

  run "$DAGGER" "do" --with 'actions: params: foo:1' -p ./plan/with test params
  assert_failure
  assert_output --partial "conflicting values string and 1"

  run "$DAGGER" "do" -p ./plan/with test params
  assert_failure
  assert_output --partial "actions.test.params.env.FOO: non-concrete value string"
}

@test "plan/platform" {
   cd "$TESTDIR"

   # Run with amd64 platform
   run "$DAGGER" "do" -p./plan/platform/config_platform_linux_amd64.cue verify

   # Run with arm64 platform
   run "$DAGGER" "do" -p./plan/platform/config_platform_linux_arm64.cue verify

   # Run with invalid platform
   run "$DAGGER" "do" -p./plan/platform/config_platform_failure_invalid_platform.cue verify
   assert_failure
}
