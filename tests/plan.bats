setup() {
    load 'helpers'

    common_setup
}

@test "plan: hello" {
  run dagger --europa up ./plan/hello-europa
  assert_success
}