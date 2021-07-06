package docker

import (
	"alpha.dagger.io/aws"
	"alpha.dagger.io/aws/ecr"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/random"
	"alpha.dagger.io/alpine"
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

	remoteImage: #RemoteImage & {
		"target": target
		source:   TestResources.image
		auth: {
			username: ecrCreds.username
			secret:   ecrCreds.secret
		}
	}
}

#TestGetSecret: {
	secret: dagger.#Artifact

	out: {
		string

		#up: [
			op.#Load & {from: alpine.#Image},

			op.#Exec & {
				always: true
				args: ["sh", "-c", "cp /input/secret /secret"]
				mount: "/input/secret": "secret": secret
			},

			op.#Export & {
				source: "/secret"
			},
		]
	}
}

TestRemoteDocker: {
	dockerConfig: {
		username: string & dagger.#Input
		secret:   dagger.#Secret & dagger.#Input
	}

	secret: #TestGetSecret & {
		secret: dockerConfig.secret
	}

	target: "daggerio/ci-test:test-docker-\(TestResources.suffix.out)"

	remoteImage: #RemoteImage & {
		"target": target
		source:   TestResources.image
		auth: {
			username: dockerConfig.username
			"secret": secret.out
		}
	}
}
