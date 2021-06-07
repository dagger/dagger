package docker

import (
	"dagger.io/docker"
	"dagger.io/dagger"
)

TestConfig: {
	host:       string         @dagger(input)
	user:       string         @dagger(input)
	key:        dagger.#Secret @dagger(input)
	passphrase: dagger.#Secret @dagger(input)
}

TestRun: {
	random: #Random & {}

	run: docker.#Run & {
		name: "daggerci-test-simple-\(random.out)"
		ref:  "hello-world"

		ssh: {
			host:          TestConfig.host
			user:          TestConfig.user
			key:           TestConfig.key
			keyPassphrase: TestConfig.passphrase
		}
	}
}
