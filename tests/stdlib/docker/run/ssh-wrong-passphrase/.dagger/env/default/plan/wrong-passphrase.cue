package docker

import (
	"dagger.io/docker"
	"dagger.io/dagger"
)

testConfig: {
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
			host:          testConfig.host
			user:          testConfig.user
			key:           testConfig.key
			keyPassphrase: testConfig.passphrase
		}
	}
}
