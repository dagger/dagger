package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
)

TestRegistry: {
	username: dagger.#Input & {string}
	secret:   dagger.#Input & {dagger.#Secret}
}

TestPush: {
	// Generate a random string
	// Seed is used to force buildkit execution and not simply use a previous generated string.
	tag: random.#String & {seed: "docker push and pull should fail"}

	target: "daggerio/ci-test:\(tag.out)"

	image: #Build & {
		dockerfile: """
				FROM alpine
				RUN echo "test" > /test.txt
			"""
		source: ""
	}

	push: #Push & {
		"target": target
		source:   image
		auth: {
			username: TestRegistry.username
			secret:   TestRegistry.secret
		}
	}
}
