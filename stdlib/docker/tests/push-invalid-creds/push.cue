package docker

import (
	"alpha.dagger.io/random"
)

TestRegistry: {
	username: string @dagger(input)
	secret:   string @dagger(input)
}

TestPush: {
	// Generate a random string
	// Seed is used to force buildkit execution and not simply use a previous generated string.
	tag: random.#String & {seed: "docker push and pull should fail"}

	target: "daggerio/ci-test:\(tag.out)"

	image: #ImageFromDockerfile & {
		dockerfile: """
				FROM alpine
				RUN echo "test" > /test.txt
			"""
		context: ""
	}

	remoteImage: #RemoteImage & {
		"target": target
		source:   image
		auth: {
			username: TestRegistry.username
			secret:   TestRegistry.secret
		}
	}
}
