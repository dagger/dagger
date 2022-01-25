setup() {
    load '../../../bats_helpers'

    common_setup
}

@test "aws.#CLI" {
    dagger up ./default_version.cue
    dagger up ./sts_get_caller_identity.cue
}