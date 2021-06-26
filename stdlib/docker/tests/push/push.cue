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
	tag: random.#String & {seed: "docker push"}

	name: "daggerio/ci-test:\(tag.out)"

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
		"name": name
		source: image
		auth: {
			username: TestRegistry.username
			"secret": secret.out
		}
	}
}
