setup() {
    load 'helpers'

    common_setup

    # These tests generally do not benefit from caching
    # as they only create small files
    unset DAGGER_CACHE_FROM
    unset DAGGER_CACHE_TO
}

@test "plan/do: action flags help output" {
  run "$DAGGER" "do" -p ./plan/do/actions.cue
  assert_success
  assert_output --partial "<action> [subaction...] [flags]"
}

@test "plan/do: action sanity checks" {
  run "$DAGGER" "do" -p ./plan/do/actions.cue not exist
  assert_failure
  assert_output --partial "not found"
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

@test "plan/do: check flags" {
  run "$DAGGER" "do" -p ./plan/do/do_flags.cue test --help
  assert_output --partial "--doit"
  assert_output --partial "--message string"
  assert_output --partial "--name string"
  assert_output --partial "--num float"
}

@test "plan/hello" {
  # Europa loader handles the cwd differently, therefore we need to CD into the tree at or below the parent of cue.mod
  cd "$TESTDIR"
  "$DAGGER" "do" -p ./plan/hello-europa test
}

@test "plan/client/filesystem/read: fs/usage" {
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

@test "plan/client/filesystem/read: fs/not_exists" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/not_exists"

  run "$DAGGER" "do" -p . test
  assert_failure
  assert_output --partial 'path "/foobar" does not exist'
}


@test "plan/client/filesystem/read: fs/invalid_fs_input" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/invalid_fs_input"

  run "$DAGGER" "do" -p . test
  assert_failure
  assert_output --partial 'test.txt" is not a directory'
}


@test "plan/client/filesystem/read: fs/invalid_fs_type" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/invalid_fs_type"

  run "$DAGGER" "do" -p . test
  assert_failure
  assert_output --partial 'rootfs" cannot be a directory'
}

@test "plan/client/filesystem/read: fs/relative" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/relative"

  "$DAGGER" "do" -p . test valid

  run "$DAGGER" "do" -p . test notIncluded
  assert_failure
  assert_output --partial 'test.log: no such file or directory'
}

@test "plan/client/filesystem/read: fs/multiple" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/multiple"

  "$DAGGER" "do" -p test.cue test
}

@test "plan/client/filesystem/read: fs/dynamic" {
  cd "$TESTDIR/plan/client/filesystem/read/fs/dynamic"

  export TMP_DIR_PATH=$BATS_TEST_TMPDIR

  run "$DAGGER" "do" -p fail_load.cue test
  assert_failure
  assert_output --partial 'not supported'

  run "$DAGGER" "do" -p fail_path_exists.cue test
  assert_failure
  assert_output --partial 'not supported'
}

@test "plan/client/filesystem/read: file" {
  cd "$TESTDIR/plan/client/filesystem/read/file"

  export TEST_FILE_PATH="test.txt"
  "$DAGGER" "do" -p . test usage

  run "$DAGGER" "do" -p . test concrete
  assert_failure
  assert_output --partial "unexpected concrete value"
}

@test "plan/client/filesystem/write: fs" {
  cd "$TESTDIR/plan/client/filesystem/write"

  rm -rf "./out_fs"

  "$DAGGER" "do" -p . test fs
  assert [ "$(cat ./out_fs/test)" = "foobar" ]

  rm -rf "./out_fs"
}

@test "plan/client/filesystem/write: files" {
  cd "$TESTDIR/plan/client/filesystem/write"

  mkdir -p ./out_files
  rm -rf ./out_files/*

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

  # -- exec --
  # This test is focused on ensuring that the export doesn't cause
  # duplicated output. Easiest to just use external grep for this
  run sh -c '"$DAGGER" "do" --log-format=plain -l info -p ./ test exec 2>&1 | grep -c "hello world"'
  assert_output 1
  assert [ "$(cat ./out_files/execTest/output.txt)" = "hello world" ]

  rm -rf ./out_files
}

@test "plan/client/filesystem/write: multiple" {
  export TMP_DIR=$BATS_TEST_TMPDIR

  "$DAGGER" "do" -p ./plan/client/filesystem/write/multiple/test.cue test

  run ls -1 "$TMP_DIR"
  assert_output "bar.txt
foo.txt"
}

@test "plan/client/filesystem: update" {
  cd "$TESTDIR/plan/client/filesystem/conflict"

  echo -n foo > test.txt
  run "$DAGGER" "do" -p ./test.cue test
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

@test "plan/client/env optional set" {
  cd "${TESTDIR}"

  export TEST_DEFAULT="hello universe"
  export TEST_OPTIONAL="foobar"

  "$DAGGER" "do" -p ./plan/client/env/optional.cue test set
}

@test "plan/client/env optional unset" {
  cd "${TESTDIR}"

  "$DAGGER" "do" -p ./plan/client/env/optional.cue test unset
}

@test "plan/client/env not exists" {
  cd "${TESTDIR}"

  run "$DAGGER" "do" -p ./plan/client/env/usage.cue test
  assert_failure
  assert_output --regexp "environment variable \"TEST_(STRING|DEFAULT|SECRET)\" not set"
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
  assert_output --partial "conflicting values"
}

@test "plan/outputs" {
  cd "${TESTDIR}/plan/outputs"

  run "$DAGGER" "do" -l error --output-format plain test empty
  refute_output

  run "$DAGGER" "do" --output-format plain test simple
  assert_line --regexp 'digest[\ ]+"sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"'

  run "$DAGGER" "do" --output-format yaml test simple
  assert_output --partial "digest: sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"

  "$DAGGER" "do" test control | jq -re 'keys == ["bar", "cmd", "foo", "int", "transf"] and .foo == .bar and .foo == .transf and .cmd == "/bin/sh" and .int == 42'
}

@test "plan/platform" {

   cd "$TESTDIR"

   # Run with invalid platform format
   run "$DAGGER" "do" --experimental --platform invalid -p./plan/platform/platform.cue test
   assert_failure
   assert_output --partial "unknown operating system or architecture: invalid argument"


   # Require --experimental flag
   run "$DAGGER" "do" --platform linux/arm64 -p./plan/platform/platform.cue test
   assert_failure
   assert_output --partial "--platform requires --experimental flag"


   # Run with non-existing platform
   run "$DAGGER" "do" --experimental --platform invalid/invalid -p./plan/platform/platform.cue test
   assert_failure
   assert_output --partial "no match for platform in manifest"
}

@test "plan/do: invalid BUILDKIT_HOST results in error" {
   cd "$TESTDIR"

   # ip address is in a reserved range that should be unroutable
   export BUILDKIT_HOST=tcp://192.0.2.1:1234
   run timeout 30 "$DAGGER" "do" -p ./plan/do/actions.cue frontend test
   assert_failure
   assert_output --partial "Unavailable: connection error"
}

@test "plan/do: cache" {
   cd "$TESTDIR"

   unset ACTIONS_RUNTIME_URL
   unset ACTIONS_RUNTIME_TOKEN
   unset ACTIONS_CACHE_URL

   export DAGGER_CACHE_FROM=type=gha,scope=dagger-ci-foo-bar-test-scope
   run "$DAGGER" "do" -p ./plan/do/actions.cue frontend test
   assert_failure
   assert_output --partial "missing github actions token"
   unset DAGGER_CACHE_FROM

   export DAGGER_CACHE_TO=type=gha,scope=dagger-ci-foo-bar-test-scope
   run "$DAGGER" "do" -p ./plan/do/actions.cue frontend test
   assert_failure
   assert_output --partial "missing github actions token"
   unset DAGGER_CACHE_TO

   export DAGGER_CACHE_FROM=type=gha,scope=dagger-ci-foo-bar-test-scope,token=xyz
   run "$DAGGER" "do" -p ./plan/do/actions.cue frontend test
   assert_failure
   assert_output --partial "missing github actions cache url"
   unset DAGGER_CACHE_FROM

   export DAGGER_CACHE_TO=type=gha,scope=dagger-ci-foo-bar-test-scope,token=xyz
   run "$DAGGER" "do" -p ./plan/do/actions.cue frontend test
   assert_failure
   assert_output --partial "missing github actions cache url"
   unset DAGGER_CACHE_TO
}

@test "plan/validate/concrete" {
  cd "$TESTDIR"

  run "$DAGGER" "do" -p ./plan/validate/concrete/definition.cue test
  assert_failure
  assert_output --partial '"actions.test.required" is not set'
  assert_output --partial './plan/validate/concrete/definition.cue'

  run "$DAGGER" "do" -p ./plan/validate/concrete/reference.cue test
  assert_failure
  assert_output --partial '"actions.test._ref" is not set'
  assert_output --partial './plan/validate/concrete/reference.cue'

  run "$DAGGER" "do" -p ./plan/validate/concrete/fs.cue test
  assert_failure
  assert_output --partial '"actions.test.required" is not set'
  assert_output --partial './plan/validate/concrete/fs.cue'

  run "$DAGGER" "do" -p ./plan/validate/concrete/task.cue test
  assert_failure
  assert_output --partial '"actions.test.path" is not set'
  assert_output --partial './plan/validate/concrete/task.cue'

  run "$DAGGER" "do" -p ./plan/validate/concrete/multitype.cue test
  assert_failure
  assert_output --partial '"actions.test.required" is not set'
  assert_output --partial './plan/validate/concrete/multitype.cue'

  run "$DAGGER" "do" -p ./plan/validate/concrete/docker_image.cue test
  assert_failure
  assert_output --partial '"actions.test.input" is not set'
  assert_output --partial './plan/validate/concrete/docker_image.cue'

  run "$DAGGER" "do" -p ./plan/validate/concrete/yarn.cue test
  assert_failure
  assert_output --partial '"actions.test.source" is not set'
  assert_output --partial './plan/validate/concrete/yarn.cue'

  run "$DAGGER" "do" -p ./plan/validate/concrete/struct_or_other.cue test
  assert_success

  # https://github.com/dagger/dagger/issues/2363
  run "$DAGGER" "do" -p ./plan/validate/concrete/clientenv.cue test
  assert_success

  run "$DAGGER" "do" -p ./plan/validate/concrete/clientenv_default.cue test
  assert_success

  run "$DAGGER" "do" -p ./plan/validate/concrete/clientenv_missing.cue test
  assert_failure
  assert_output --partial "actions.test.site: undefined field: NONEXISTENT:"
}

@test "plan/validate/undefined" {
  run "$DAGGER" "do" -p ./plan/validate/undefined/undefined.cue test
  assert_failure
  assert_output --partial 'actions.test.undefinedAction.input: undefined field: nonexistent'
  assert_output --partial 'actions.test.undefinedDef: undefined field: #NonExistent'
  assert_output --partial 'actions.test.filesystem.input: undefined field: "/non/existent":'

  # FIXME: This is currently broken and yields an `incomplete cause disjunction`
  # assert_output --partial 'actions.test.disjunction: undefined field: #NonExistent:'
  refute_output --partial 'actions.test.disjunction: incomplete cause disjunction'
}
