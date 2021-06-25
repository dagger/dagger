package docker

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
)

TestConfig: {
	host: string         @dagger(input)
	user: string         @dagger(input)
	key:  dagger.#Secret @dagger(input)
}

TestSSH: client: docker.#Command & {
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
