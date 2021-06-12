package docker

import (
	"dagger.io/dagger/op"
	"dagger.io/dagger"
	"dagger.io/docker"
	"dagger.io/alpine"
	"dagger.io/random"
)

TestRegistry: {
	username: string         @dagger(input)
	secret:   dagger.#Secret @dagger(input)
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

TestPush: {
	tag: random.#String & {seed: "docker push and pull"}

	name: "daggerio/ci-test:\(tag.out)"

	secret: #TestGetSecret & {
		secret: TestRegistry.secret
	}

	image: docker.#ImageFromDockerfile & {
		dockerfile: """
				FROM alpine
				RUN echo "test" > /test.txt
			"""
		context: ""
	}

	push: docker.#Push & {
		"name": name
		source: image
		registry: {
			username: TestRegistry.username
			"secret": secret.out
		}
	}
}
