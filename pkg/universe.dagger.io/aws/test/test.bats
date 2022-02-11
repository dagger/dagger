setup() {
    load '../../bats_helpers'

    common_setup
}

@test "aws" {
    dagger up ./default_version.cue
    dagger up ./credentials.cue
    dagger up ./config_file.cue
}
