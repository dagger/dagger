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

@test "doc-1004-first-env" {
    setup_example_sandbox "doc"

    # Follow tutorial
    mkdir multibucket
    cp $CODEBLOC_SRC/multibucket/source.cue multibucket
    cp $CODEBLOC_SRC/multibucket/yarn.cue multibucket
    cp $CODEBLOC_SRC/multibucket/netlify.cue multibucket

    dagger doc alpha.dagger.io/netlify
    dagger doc alpha.dagger.io/js/yarn

    # Initialize new env
    dagger new 'multibucket' -p ./multibucket

    # Check inputs
    dagger input list -e multibucket

    # Copy corresponding env
    cp -r $CODEBLOC_SRC/.dagger/env/multibucket .dagger/env/
    # Add missing src input
    dagger -e multibucket input dir src . 

    # Run test
    dagger -e multibucket up
    url=$(dagger -e multibucket query -f text site.netlify.deployUrl)

    # Check output :
    run curl $url
    assert_output --partial "./static/css/main.9149988f.chunk.css"
}