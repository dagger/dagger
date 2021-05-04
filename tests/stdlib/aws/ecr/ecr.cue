package ecr

import (
	"dagger.io/aws"
	"dagger.io/aws/ecr"
	"dagger.io/alpine"
	"dagger.io/dagger/op"
)

TestConfig: awsConfig: aws.#Config & {
	region: "us-east-2"
}

// Generate a random number
random: {
	string
	#up: [
		op.#Load & {from: alpine.#Image},
		op.#Exec & {
			args: ["sh", "-c", "cat /dev/urandom | tr -dc 'a-z' | fold -w 10 | head -n 1 | tr -d '\n' > /rand"]
		},
		op.#Export & {
			source: "/rand"
		},
	]
}

TestECR: {
	repository: "125635003186.dkr.ecr.\(TestConfig.awsConfig.region).amazonaws.com/dagger-ci"
	tag:        "test-ecr-\(random)"

	creds: ecr.#Credentials & {
		config: TestConfig.awsConfig
	}

	push: {
		ref: "\(repository):\(tag)"

		#up: [
			op.#DockerBuild & {
				dockerfile: """
				FROM alpine
				RUN echo \(random) > /test
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
				"sh", "-c", "test $(cat test) = \(random)",
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
				RUN test $(cat test) = \#(random)
			"""#
		},
	]
}
