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

@test "doc-1006-google-cloud-run" {
    setup_example_sandbox "doc"

    mkdir gcpcloudrun
    cp $CODEBLOC_SRC/gcpcloudrun/source.cue gcpcloudrun

    # Initialize new env
    dagger new 'gcpcloudrun' -p gcpcloudrun

    # Copy corresponding env
    cp -r $CODEBLOC_SRC/.dagger/env/gcpcloudrun .dagger/env/
    # Add missing src input
    dagger -e gcpcloudrun input dir src .

    # Run test
    run dagger -e gcpcloudrun up
    assert_success
}

@test "doc-1008-aws-cloudformation" {
    setup_example_sandbox "doc"

    ### Create a basic plan
    ## Construct
    mkdir cloudformation
    cp $CODEBLOC_SRC/cloudformation/template.cue cloudformation

    # Cloudformation relay
    dagger doc alpha.dagger.io/aws/cloudformation
    cp $CODEBLOC_SRC/cloudformation/source-begin.cue cloudformation/source.cue

    # Initialize new env
    dagger new 'cloudformation' -p cloudformation

    # Finish template setup
    cp $CODEBLOC_SRC/cloudformation/source-end.cue cloudformation/source.cue
    # Copy corresponding env
    cp -r $CODEBLOC_SRC/.dagger/env/cloudformation .dagger/env/

    # Run test
    dagger -e cloudformation up
    stackName=$(dagger -e cloudformation query cfnStackName -f text)

    ## Cleanup
    # Place back empty source
    cp $CODEBLOC_SRC/cloudformation/source-begin.cue cloudformation/source.cue
    cp $CODEBLOC_SRC/cloudformation/deletion.cue cloudformation/deletion.cue
    # Prepare and run cloudformation cleanup
    dagger -e cloudformation input text stackRemoval.stackName $stackName
    dagger -e cloudformation up

    ### Template part
    ## Create convert.cue
    cp $CODEBLOC_SRC/cloudformation/template/convert.cue cloudformation/convert.cue
    rm cloudformation/source.cue cloudformation/deletion.cue

    ## Retrieve Unmarshalled JSON
    dagger query -e cloudformation s3Template

    ## Remove convert.cue
    rm cloudformation/convert.cue
    ## Store the output
    cp $CODEBLOC_SRC/cloudformation/template/template-begin.cue cloudformation/template.cue
    # Inspect conf
    dagger query -e cloudformation template -f text

    cp $CODEBLOC_SRC/cloudformation/template/deployment.cue cloudformation/deployment.cue
    cp $CODEBLOC_SRC/cloudformation/template/template-end.cue cloudformation/template.cue
    cp $CODEBLOC_SRC/cloudformation/source-end.cue cloudformation/source.cue

    # Deploy again
    dagger -e cloudformation query template -f text
    dagger -e cloudformation up
    dagger -e cloudformation output list

    ## Cleanup again
    stackName=$(dagger -e cloudformation query cfnStackName -f text)
    rm -rf cloudformation/*
    # Place back empty source
    cp $CODEBLOC_SRC/cloudformation/source-begin.cue cloudformation/source.cue
    cp $CODEBLOC_SRC/cloudformation/deletion.cue cloudformation/deletion.cue
    # Prepare and run cloudformation cleanup
    dagger -e cloudformation input text stackRemoval.stackName $stackName
    dagger -e cloudformation up
}