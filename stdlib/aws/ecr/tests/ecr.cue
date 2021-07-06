package ecr

import (
	"alpha.dagger.io/aws"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/random"
)

TestConfig: awsConfig: aws.#Config & {
	region: "us-east-2"
}

TestECR: {
	suffix: random.#String & {
		seed: ""
	}

	repository: "125635003186.dkr.ecr.\(TestConfig.awsConfig.region).amazonaws.com/dagger-ci"
	tag:        "test-ecr-\(suffix.out)"

	creds: #Credentials & {
		config: TestConfig.awsConfig
	}

	remoteImage: {
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

	image: #up: [
		op.#DockerLogin & {
			target:   remoteImage.ref
			username: creds.username
			secret:   creds.secret
		},

		op.#FetchContainer & {
			ref: remoteImage.ref
		},
	]

	test: #up: [
		op.#Load & {
			from: image
		},

		op.#Exec & {
			always: true
			args: [
				"sh", "-c", "test $(cat test) = \(suffix.out)",
			]
		},
	]

	testBuild: #up: [
		op.#DockerLogin & {
			target:   remoteImage.ref
			username: creds.username
			secret:   creds.secret
		},

		op.#DockerBuild & {
			dockerfile: #"""
				FROM \#(remoteImage.ref)
				RUN test $(cat test) = \#(suffix.out)
			"""#
		},
	]
}
