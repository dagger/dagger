setup() {
    load 'helpers'

    common_setup
}

@test "op.#Load" {
    run "$DAGGER" compute "$TESTDIR"/ops/load/valid/component
    assert_success
    assert_line '{"component":{},"test1":"lol","test2":"lol"}'

    "$DAGGER" compute "$TESTDIR"/ops/load/valid/script

    run "$DAGGER" compute "$TESTDIR"/ops/load/invalid/cache
    assert_failure
}

@test "op.#Mount" {
    # tmpfs
    "$DAGGER" compute "$TESTDIR"/ops/mounts/valid/tmpfs

    # cache
    "$DAGGER" compute "$TESTDIR"/ops/mounts/valid/cache

    # component
    run "$DAGGER" compute "$TESTDIR"/ops/mounts/valid/component
    assert_success
    assert_line '{"test":"hello world"}'

    # FIXME https://github.com/blocklayerhq/dagger/issues/46
    # "$DAGGER" compute "$TESTDIR"/ops/mounts/valid/script
}

@test "op.#Copy" {
    run "$DAGGER" compute "$TESTDIR"/ops/copy/valid/component
    assert_success
    assert_line '{"component":{},"test1":"lol","test2":"lol"}'

    "$DAGGER" compute "$TESTDIR"/ops/copy/valid/script

    # FIXME https://github.com/blocklayerhq/dagger/issues/44
    # run "$DAGGER" compute "$TESTDIR"/ops/copy/invalid/cache
    # assert_failure
}

@test "op.#Local" {
    skip "There are no local tests right now (the feature is possibly not functioning at all: see https://github.com/blocklayerhq/dagger/issues/41)"
}

@test "op.#FetchContainer" {
    # non existent container image"
    run "$DAGGER" compute "$TESTDIR"/ops/fetch-container/nonexistent/image
    assert_failure

    # non existent container tag
    run "$DAGGER" compute "$TESTDIR"/ops/fetch-container/nonexistent/tag
    assert_failure

    # non existent container digest
    run "$DAGGER" compute "$TESTDIR"/ops/fetch-container/nonexistent/digest
    assert_failure

    # valid containers
    run "$DAGGER" compute "$TESTDIR"/ops/fetch-container/exist
    assert_success

    # missing ref
    # FIXME: distinguish missing inputs from incorrect config
    # run "$DAGGER" compute "$TESTDIR"/ops/fetch-container/invalid
    # assert_failure

    # non existent container image with valid digest
    # FIXME https://github.com/blocklayerhq/dagger/issues/32
    # run "$DAGGER" compute "$TESTDIR"/ops/fetch-container/nonexistent/image-with-valid-digest
    # assert_failure
}

@test "op.#PushContainer" {
    skip_unless_secrets_available "$TESTDIR"/ops/push-container/inputs.yaml

    "$DAGGER" compute --input-yaml "$TESTDIR"/ops/push-container/inputs.yaml "$TESTDIR"/ops/push-container
}

@test "op.#FetchGit" {
    run "$DAGGER" compute "$TESTDIR"/ops/fetch-git/exist
    assert_success

    run "$DAGGER" compute "$TESTDIR"/ops/fetch-git/nonexistent/remote
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/ops/fetch-git/nonexistent/ref
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/ops/fetch-git/nonexistent/bork
    assert_failure

    # FIXME: distinguish missing inputs from incorrect config
    # run "$DAGGER" compute "$TESTDIR"/ops/fetch-git/invalid
    # assert_failure
}

@test "op.#Exec" {
    run "$DAGGER" compute "$TESTDIR"/ops/exec/invalid
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/ops/exec/error
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/ops/exec/simple
    assert_success

    # XXX should run twice and test that the string "always output" is visible with DOCKER_OUTPUT=1
    # Alternatively, use export, but this would test multiple things then...
    run "$DAGGER" compute "$TESTDIR"/ops/exec/always
    assert_success

    run "$DAGGER" compute "$TESTDIR"/ops/exec/env/invalid
    assert_failure

    run "$DAGGER" compute  "$TESTDIR"/ops/exec/env/valid
    assert_success

    run "$DAGGER" compute --input-string 'bar=overlay environment' "$TESTDIR"/ops/exec/env/overlay
    assert_success

    run "$DAGGER" compute  "$TESTDIR"/ops/exec/dir/doesnotexist
    assert_success

    run "$DAGGER" compute  "$TESTDIR"/ops/exec/dir/exist
    assert_success


    run "$DAGGER" compute "$TESTDIR"/ops/exec/undefined/non_concrete_referenced
    assert_success
    assert_line '{"hello":"world"}'

    # NOTE: the exec is meant to fail - and we test that as a way to confirm it has been executed
    run "$DAGGER" compute "$TESTDIR"/ops/exec/undefined/non_concrete_not_referenced
    assert_failure

    # package with optional def, not referenced, should be executed
    run "$DAGGER" compute "$TESTDIR"/ops/exec/undefined/with_pkg_def
    assert_success

    # script with optional prop, not referenced, should be executed
    run "$DAGGER" compute "$TESTDIR"/ops/exec/undefined/with_pkg_optional
    assert_success

    # FIXME https://github.com/blocklayerhq/dagger/issues/74
    # run "$DAGGER" compute "$TESTDIR"/ops/exec/exit_code
    # assert_failure # --exit=123

    # script with non-optional prop, not referenced, should be executed
    # FIXME https://github.com/blocklayerhq/dagger/issues/70
    # run "$DAGGER" compute "$TESTDIR"/ops/exec/undefined/with_pkg_mandatory
    # assert_failure
}

@test "op.#Export" {
    run "$DAGGER" compute "$TESTDIR"/ops/export/json
    assert_success
    assert_line '{"testMap":{"something":"something"},"testScalar":true}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/string
    assert_success
    assert_line '{"test":"something"}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/withvalidation
    assert_success
    assert_line '{"test":"something"}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/concurrency
    assert_success

    # does not pass additional validation
    run "$DAGGER" compute "$TESTDIR"/ops/export/invalid/validation
    assert_failure

    # invalid format
    run "$DAGGER" compute "$TESTDIR"/ops/export/invalid/format
    assert_failure

    # invalid path
    run "$DAGGER" compute "$TESTDIR"/ops/export/invalid/path
    assert_failure


    run "$DAGGER" compute "$TESTDIR"/ops/export/float
    assert_success
    assert_line '{"test":-123.5}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/yaml
    assert_success
    assert_line '{"testMap":{"something":"something"},"testScalar":true}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/bool
    assert_success
    assert_line '{"test":true}'

    # FIXME: https://github.com/blocklayerhq/dagger/issues/96
    # run "$DAGGER" compute "$TESTDIR"/ops/export/number
    # assert_success
    # assert_line '{"test":-123.5}'
}

@test "op.#Subdir" {
    run "$DAGGER" compute "$TESTDIR"/ops/subdir/simple
    assert_success
    assert_line '{"hello":"world"}'
}

@test "op.#DockerBuild" {
    run "$DAGGER" compute --input-dir TestData="$TESTDIR"/ops/dockerbuild/testdata "$TESTDIR"/ops/dockerbuild
    assert_success
}
