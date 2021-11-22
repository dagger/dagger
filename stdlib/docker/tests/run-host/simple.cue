package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
)

TestConfig: {
	host: string @dagger(input)
}

TestHost: {
	suffix: random.#String & {
		seed: ""
	}

	run: #Run & {
		name: "daggerci-test-ssh-\(suffix.out)"
		ref:  "hello-world"
		host: TestConfig.host
	}
}
