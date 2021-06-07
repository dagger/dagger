package gcr

import (
	"dagger.io/gcp"
	"dagger.io/gcp/gcr"
	"dagger.io/dagger/op"
)

TestConfig: gcpConfig: gcp.#Config

TestGCR: {
	random: #Random & {}

	repository: "gcr.io/dagger-ci/test"
	tag:        "test-gcr-\(random.out)"

	creds: gcr.#Credentials & {
		config: TestConfig.gcpConfig
	}

	push: {
		ref: "\(repository):\(tag)"

		#up: [
			op.#DockerBuild & {
				dockerfile: """
				FROM alpine
				RUN echo \(random.out) > /test
				"""
			},

			op.#DockerLogin & {
				target:   repository
				username: creds.username
				secret:   creds.secret
			},

			op.#PushContainer & {
				"ref": ref
			},
		]
	}

	pull: #up: [
		op.#DockerLogin & {
			target:   push.ref
			username: creds.username
			secret:   creds.secret
		},

		op.#FetchContainer & {
			ref: push.ref
		},
	]

	verify: #up: [
		op.#Load & {
			from: pull
		},

		op.#Exec & {
			always: true
			args: [
				"sh", "-c", "test $(cat test) = \(random.out)",
			]
		},
	]

	verifyBuild: #up: [
		op.#DockerLogin & {
			target:   push.ref
			username: creds.username
			secret:   creds.secret
		},

		op.#DockerBuild & {
			dockerfile: #"""
				FROM \#(push.ref)
				RUN test $(cat test) = \#(random.out)
			"""#
		},
	]
}
