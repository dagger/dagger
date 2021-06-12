package docker

import (
	"dagger.io/docker"
	"dagger.io/random"
)

TestRegistry: {
	username: string @dagger(input)
	secret:   string @dagger(input)
}

TestPush: {
	tag: random.#String & {seed: "docker push and pull should fail"}

	name: "daggerio/ci-test:\(tag.out)"

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
			secret:   TestRegistry.secret
		}
	}
}
