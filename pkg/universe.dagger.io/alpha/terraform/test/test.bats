setup() {
    load '../../../bats_helpers'

    common_setup
}

@test "terraform: default version" {
    dagger "do" test
}

@test "terraform: specific version" {
    dagger "do" test \
      --with 'actions: test: versionWorkflow: terraformVersion: "1.2.6"' \
      --with 'actions: test: versionWorkflow: verify: regex: "1\\.2\\.6"'
}
