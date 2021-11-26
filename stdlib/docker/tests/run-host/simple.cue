package docker

import (
	"alpha.dagger.io/random"
)

TestConfig: {
	host: string @dagger(input)
}

TestHost: {
	suffix: random.#String & {
		seed: "docker-tcp-test"
	}

	run: #Run & {
		name: "daggerci-test-tcp-\(suffix.out)"
		ref:  "hello-world"
		host: TestConfig.host
	}
}
