
setup() {
    load 'node_modules/bats-assert/load'
}

function dagger() {
	"${DAGGER_BINARY:-$(which dagger)}" "$@"
}

@test "netlify" {
    dagger -e netlify up
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

@test "docker run: ssh" {
	dagger -e docker-run-ssh up
}

@test "docker run: ssh with passphrase" {
	dagger -e docker-run-ssh-passphrase up
}

@test "docker run: ssh with wrong passphrase" {
	run dagger -e docker-run-ssh-wrong-passphrase up
	assert_failure
}

@test "google cloud: gcr" {
	dagger -e google-gcr up
}

@test "google cloud: gke" {
	dagger -e google-gke up
}
