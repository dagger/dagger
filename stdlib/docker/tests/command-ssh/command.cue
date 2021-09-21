package docker

import (
	"alpha.dagger.io/dagger"
)

TestConfig: {
	host: string & dagger.#Input
	user: string & dagger.#Input
	key:  dagger.#Secret & dagger.#Input
}

TestSSH: client: #Command & {
	command: #"""
			docker $CMD
		"""#
	ssh: {
		host: TestConfig.host
		user: TestConfig.user
		key:  TestConfig.key
	}
	env: CMD: "version"
}
