## Doc commands are being extracted from this file and helpers.
## Indentation is important, please append at the end

setup() {
    load 'helpers'

    common_setup
}

#  Test 1003-get-started
@test "doc-1003-get-started" {
    setup_example_sandbox "doc"

    # Set examples private key
    ./import-tutorial-key.sh

    # Collect url
    dagger up
    url=$(dagger query -f text url)

    # More commands
    dagger list
    ls -l ./s3
    dagger input list

    # Check output
    run curl $url
    assert_output --partial "My Todo app"
}