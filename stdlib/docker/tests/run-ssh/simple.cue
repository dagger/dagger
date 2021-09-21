package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/random"
)

TestConfig: {
	host: string & dagger.#Input
	user: string & dagger.#Input
	key:  dagger.#Secret & dagger.#Input
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
