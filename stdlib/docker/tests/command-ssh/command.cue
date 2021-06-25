package docker

import (
	"alpha.dagger.io/dagger"
)

TestConfig: {
	host: string         @dagger(input)
	user: string         @dagger(input)
	key:  dagger.#Secret @dagger(input)
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
