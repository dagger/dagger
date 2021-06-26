package docker

import (
	"alpha.dagger.io/random"
)

TestRegistry: {
	username: string @dagger(input)
	secret:   string @dagger(input)
}

TestPush: {
	tag: random.#String & {seed: "docker push and pull should fail"}

	name: "daggerio/ci-test:\(tag.out)"

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
			secret:   TestRegistry.secret
		}
	}
}
