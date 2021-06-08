package main

import (
	"dagger.io/gcp"
	"dagger.io/gcp/gcr"
	"dagger.io/dagger/op"
	"dagger.io/random"
)

TestConfig: gcpConfig: gcp.#Config

TestGCR: {
	suffix: random.#String & {
		seed: ""
	}

	repository: "gcr.io/dagger-ci/test"
	tag:        "test-gcr-\(suffix.out)"

	creds: gcr.#Credentials & {
		config: TestConfig.gcpConfig
	}

	push: {
		ref: "\(repository):\(tag)"

		#up: [
			op.#DockerBuild & {
				dockerfile: """
				FROM alpine
				RUN echo \(suffix.out) > /test
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
				"sh", "-c", "test $(cat test) = \(suffix.out)",
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
				RUN test $(cat test) = \#(suffix.out)
			"""#
		},
	]
}
