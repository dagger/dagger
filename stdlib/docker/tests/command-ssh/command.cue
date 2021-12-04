package docker

import (
	"alpha.dagger.io/dagger"
)

TestConfig: {
	host: dagger.#Input & {string}
	user: dagger.#Input & {string}
	key:  dagger.#Input & {dagger.#Secret}
}

TestPassword: dagger.#Input & {dagger.#Secret}

TestSSH: client: #Command & {
	command: #"""
			docker $CMD && [ -f /run/secrets/password ]
		"""#
	ssh: {
		host: TestConfig.host
		user: TestConfig.user
		key:  TestConfig.key
	}
	secret: "/run/secrets/password": TestPassword
	env: CMD:                        "version"
}
