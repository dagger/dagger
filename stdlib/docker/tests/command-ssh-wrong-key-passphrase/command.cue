package docker

import (
	"alpha.dagger.io/dagger"
)

TestConfig: {
	host:          string         @dagger(input)
	user:          string         @dagger(input)
	key:           dagger.#Secret @dagger(input)
	keyPassphrase: dagger.#Secret @dagger(input)
}

TestSSH: client: #Command & {
	command: #"""
			docker version
		"""#
	sshConfig: {
		host:          TestConfig.host
		user:          TestConfig.user
		key:           TestConfig.key
		keyPassphrase: TestConfig.keyPassphrase
	}
}
