setup() {
    load '../../bats_helpers'

    common_setup
}

@test "aws" {
    dagger "do" -p ./default_version.cue getVersion
    dagger "do" -p ./credentials.cue verify
    dagger "do" -p ./config_file.cue verify
}
