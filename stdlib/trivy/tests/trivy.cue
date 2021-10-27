package trivy

import (
	"alpha.dagger.io/aws"
	"alpha.dagger.io/aws/ecr"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/gcp"
	"alpha.dagger.io/gcp/gcr"
	"alpha.dagger.io/random"
)

TestConfig: awsConfig: aws.#Config & {
	region: "us-east-2"
}

TestConfig: gcpConfig: gcp.#Config & {
	project: "dagger-ci"
	region:  "us-west2-a"
}

TestConfig: {
	trivyNoAuth: #Config

	trivyBasicAuth: #Config & {
		basicAuth: {
			username: "guilaume1234"
			password: dagger.#Input & {dagger.#Secret}
		}
	}

	trivyAWSAuth: #Config & {
		awsAuth: TestConfig.awsConfig
	}

	trivyGCPAuth: #Config & {
		gcpAuth: TestConfig.gcpConfig
	}
}

TestSuffix: random.#String & {
	seed: ""
}

TestNoAuthClient: #Image & {
	config: TestConfig.trivyNoAuth
	source: "ubuntu:21.10"
}

TestBasicAuthClient: #Image & {
	config: TestConfig.trivyBasicAuth
	source: "docker.io/guilaume1234/guillaume:latest"
}

TestAWSClient: {
	repository: "125635003186.dkr.ecr.\(TestConfig.awsConfig.region).amazonaws.com/dagger-ci"
	tag:        "test-ecr-\(TestSuffix.out)"

	creds: ecr.#Credentials & {
		config: TestConfig.awsConfig
	}

	push: {
		ref: "\(repository):\(tag)"

		#up: [
			op.#DockerBuild & {
				dockerfile: """
				FROM alpine
				RUN echo \(TestSuffix.out) > /test
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

	verify: #Image & {
		config: TestConfig.trivyAWSAuth
		source: push.ref
	}
}

TestGCPClient: {
	repository: "gcr.io/dagger-ci/test"
	tag:        "test-gcr-\(TestSuffix.out)"

	creds: gcr.#Credentials & {
		config: TestConfig.gcpConfig
	}

	push: {
		ref: "\(repository):\(tag)"

		#up: [
			op.#DockerBuild & {
				dockerfile: """
				FROM alpine
				RUN echo \(TestSuffix.out) > /test
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

	verify: #Image & {
		config: TestConfig.trivyGCPAuth
		source: push.ref
	}
}
