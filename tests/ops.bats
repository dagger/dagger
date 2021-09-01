setup() {
    load 'helpers'

    common_setup
}

@test "op.#Load" {
    run "$DAGGER" compute "$TESTDIR"/ops/load/valid/component
    assert_success
    assert_line '{"TestComponent":{},"TestComponentLoad":"lol","TestNestedLoad":"lol"}'

    run "$DAGGER" compute "$TESTDIR"/ops/load/valid/script
    assert_success

    run "$DAGGER" compute "$TESTDIR"/ops/load/invalid/cache
    assert_failure
}

@test "op.#Mount" {
    # tmpfs
    run "$DAGGER" compute "$TESTDIR"/ops/mounts/valid/tmpfs
    assert_line '{"TestMountTmpfs":"ok"}'
    assert_success

    # cache
    run "$DAGGER" compute "$TESTDIR"/ops/mounts/valid/cache
    assert_success

    # component
    run "$DAGGER" compute "$TESTDIR"/ops/mounts/valid/component
    assert_success
    assert_line '{"test":"hello world"}'

    # Invalid mount path
    run "$DAGGER" compute "$TESTDIR"/ops/mounts/valid/script
    assert_failure
}

@test "op.#Copy" {
    run "$DAGGER" compute "$TESTDIR"/ops/copy/valid/component
    assert_success
    assert_line '{"TestComponent":{},"TestComponentCopy":"lol","TestNestedCopy":"lol"}'

    run "$DAGGER" compute "$TESTDIR"/ops/copy/valid/script
    assert_success

    run "$DAGGER" compute "$TESTDIR"/ops/copy/invalid/cache
    assert_failure
}

@test "op.#Local" {
    skip "There are no local tests right now"
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
    dagger_new_with_env "$TESTDIR"/ops/push-container/
    run "$DAGGER" up -e push-container
    assert_success
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

    run "$DAGGER" compute "$TESTDIR"/ops/fetch-git/gitdir
    assert_success

    dagger_new_with_env "$TESTDIR"/ops/fetch-git/private-repo
    run "$DAGGER" up -e op-fetch-git
    assert_success

    # FIXME: distinguish missing inputs from incorrect config
    # run "$DAGGER" compute "$TESTDIR"/ops/fetch-git/invalid
    # assert_failure
}

@test "op.#FetchHTTP" {
    run "$DAGGER" compute "$TESTDIR"/ops/fetch-http/exist
    assert_success

    run "$DAGGER" compute "$TESTDIR"/ops/fetch-http/nonexistent
    assert_failure
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
    run "$DAGGER" compute "$TESTDIR"/ops/exec/undefined/with_pkg_mandatory
    assert_success
}

@test "op.#Export" {
    run "$DAGGER" compute "$TESTDIR"/ops/export/json
    assert_success
    assert_line '{"TestExportList":["milk","pumpkin pie","eggs","juice"],"TestExportMap":{"something":"something"},"TestExportScalar":true}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/string
    assert_success
    assert_line '{"TestExportString":"something"}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/withvalidation
    assert_success
    assert_line '{"TestExportStringValidation":"something"}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/concurrency
    assert_success
    assert_line '{"TestExportConcurrency1":"lol1","TestExportConcurrency10":"lol10","TestExportConcurrency2":"lol2","TestExportConcurrency3":"lol3","TestExportConcurrency4":"lol4","TestExportConcurrency5":"lol5","TestExportConcurrency6":"lol6","TestExportConcurrency7":"lol7","TestExportConcurrency8":"lol8","TestExportConcurrency9":"lol9"}'

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
    assert_line '{"TestExportFloat":-123.5}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/yaml
    assert_success
    assert_line '{"TestExportList":["milk","pumpkin pie","eggs","juice"],"TestExportMap":{"something":"something"},"TestExportScalar":true}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/bool
    assert_success
    assert_line '{"TestExportBool":true}'

    run "$DAGGER" compute "$TESTDIR"/ops/export/number
    assert_success
    assert_line '{"TestExportNumber":-123.5}'
}

@test "op.#Subdir" {
    run "$DAGGER" compute "$TESTDIR"/ops/subdir/valid/simple
    assert_success
    assert_line '{"TestSimpleSubdir":"world"}'

    run "$DAGGER" compute "$TESTDIR"/ops/subdir/valid/container
    assert_success
    assert_line '{"TestSubdirMount":"world"}'

    run "$DAGGER" compute "$TESTDIR"/ops/subdir/invalid/exec
    assert_failure

    run "$DAGGER" compute "$TESTDIR"/ops/subdir/invalid/path
    assert_failure
}

@test "op.#DockerBuild" {
    run "$DAGGER" compute --input-dir TestData="$TESTDIR"/ops/dockerbuild/testdata "$TESTDIR"/ops/dockerbuild
    assert_success
}
