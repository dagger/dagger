setup() {
    load '../../bats_helpers'

    common_setup
}

@test "docker.#Build" {
    dagger up ./nested-build-test.cue

    # FIXME: this is currently broken
    run dagger up ./multi-nested-build-test.cue
    assert_failure

    dagger up ./custom-build-step-test.cue
}
