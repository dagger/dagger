package docker

import (
	"alpha.dagger.io/aws"
	"alpha.dagger.io/aws/ecr"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
)

// 
// /!\ README /!\ 
// The objective is to push an image on multiple registries to verify
// that we correctly handle that kind of configuration
//

TestResources: {
	// Generate a random string
	// Seed is used to force buildkit execution and not simply use a previous generated string.
	suffix: random.#String & {seed: "docker multi registry"}

	image: #ImageFromDockerfile & {
		dockerfile: """
				FROM alpine
				RUN echo "test" > /test.txt
			"""
		context: ""
	}
}

TestRemoteAWS: {
	awsConfig: aws.#Config

	ecrCreds: ecr.#Credentials & {
		config: awsConfig
	}

	target: "125635003186.dkr.ecr.\(awsConfig.region).amazonaws.com/dagger-ci:test-ecr-\(TestResources.suffix.out)"

	remoteImg: #Push & {
		"target": target
		source:   TestResources.image
		auth: {
			username: ecrCreds.username
			secret:   ecrCreds.secret
		}
	}
}

TestRemoteDocker: {
	dockerConfig: {
		username: dagger.#Input & {string}
		secret:   dagger.#Input & {dagger.#Secret}
	}

	target: "daggerio/ci-test:test-docker-\(TestResources.suffix.out)"

	remoteImg: #Push & {
		"target": target
		source:   TestResources.image
		auth: {
			username: dockerConfig.username
			secret:   dockerConfig.secret
		}
	}
}
