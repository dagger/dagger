setup() {
    load '../../bats_helpers'

    common_setup
}

@test "docker.#Build" {
    dagger up ./build-simple.cue
    dagger up ./build-multi-steps.cue

    dagger up ./nested-build-test.cue

    # FIXME: this is currently broken
    run dagger up ./multi-nested-build-test.cue
    assert_failure
}

@test "docker.#Run" {
    dagger up ./run-command-test.cue
    dagger up ./run-script-test.cue
    dagger up ./run-export-file-test.cue
    dagger up ./run-export-directory-test.cue
    dagger up ./image-config-test.cue
}
