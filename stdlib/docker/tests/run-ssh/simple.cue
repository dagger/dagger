package docker

import (
	"dagger.io/dagger"
	"dagger.io/random"
)

TestConfig: {
	host: string         @dagger(input)
	user: string         @dagger(input)
	key:  dagger.#Secret @dagger(input)
}

TestSSH: {
	suffix: random.#String & {
		seed: ""
	}

	run: #Run & {
		name: "daggerci-test-ssh-\(suffix.out)"
		ref:  "hello-world"

		ssh: {
			host: TestConfig.host
			user: TestConfig.user
			key:  TestConfig.key
		}
	}
}
