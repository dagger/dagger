setup_file() {
    load 'helpers'
    setup_localstack
}

setup() {
    load 'helpers'
    common_setup
}

@test "aws: ecr/localstack" {
    # skip "Debug infinite loop"
    skip_unless_local_localstack

    dagger -e aws-ecr-localstack up
}

@test "aws: s3/localstack" {
    # skip "disabled because of inifinit loop"
    skip_unless_local_localstack

    dagger -e aws-s3-localstack up
}

