package docker

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/random"
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
	// Generate a random string
	// Seed is used to force buildkit execution and not simply use a previous generated string.
	tag: random.#String & {seed: "docker push"}

	target: "daggerio/ci-test:\(tag.out)"

	secret: #TestGetSecret & {
		secret: TestRegistry.secret
	}

	image: #ImageFromDockerfile & {
		dockerfile: """
				FROM alpine
				RUN echo "test" > /test.txt
			"""
		context: ""
	}

	push: #Push & {
		"target": target
		source:   image
		auth: {
			username: TestRegistry.username
			"secret": secret.out
		}
	}
}
