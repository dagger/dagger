# Test core Dagger features & types

setup() {
    load 'helpers'

    common_setup

    # Use native Dagger environment here
    unset DAGGER_WORKSPACE
}

@test "core: inputs" {
   # List available inputs
   run dagger -e test-core input list
   assert_success
   assert_output --partial 'name'
   assert_output --partial 'dir'

   # Set text input
   dagger -e test-core input text name Bob
   run dagger -e test-core up
   assert_success
   assert_output --partial 'Hello, Bob!'

   # Unset text input
   dagger -e test-core input unset name
   run dagger -e test-core up
   assert_success
   assert_output --partial 'Hello, world!'
}
