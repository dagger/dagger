setup() {
    load 'helpers'

    common_setup
}

@test "js/yarn" {
    dagger -e js-yarn up
}

@test "alpine" {
    dagger -e alpine up
}

@test "netlify" {
    dagger -e netlify up
}

@test "git" {
    dagger -e git up
}

@test "aws: ecr" {
    dagger -e aws-ecr up
}

@test "aws: s3" {
    dagger -e aws-s3 up
}

@test "docker run: local" {
    dagger -e docker-run-local up
}

@test "docker command: ssh" {
    dagger -e docker-command-ssh up
}

@test "docker command: ssh with key passphrase" {
    dagger -e docker-command-ssh-key-passphrase up
}

@test "docker command: ssh with wrong key passphrase" {
    run dagger -e docker-command-ssh-wrong-key-passphrase up
    assert_failure
}

@test "docker run: ssh" {
    dagger -e docker-run-ssh up
}

@test "google cloud: gcr" {
    dagger -e google-gcr up
}

@test "google cloud: gke" {
    dagger -e google-gke up
}
