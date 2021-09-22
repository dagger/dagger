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
	localMode: TestConfig.awsConfig.localMode

	suffix: random.#String & {
		seed: ""
	}

	repository: string
	if localMode == false {
		repository: "125635003186.dkr.ecr.\(TestConfig.awsConfig.region).amazonaws.com/dagger-ci"
	}
	if localMode == true {
		repository: "localhost:4510/dagger-ci"
	}
	tag: "test-ecr-\(suffix.out)"

	creds: #Credentials & {
		config: TestConfig.awsConfig
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
