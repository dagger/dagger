setup() {
    load 'helpers'

    common_setup
}

@test "plan: hello" {
  run dagger --no-cache --europa up ./plan/hello-europa
  assert_success
  assert_output --partial 'Hello Europa!'
}