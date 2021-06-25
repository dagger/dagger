package compose

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/docker/compose"
)

repo: dagger.#Artifact @dagger(input)

TestCompose: {
	up: compose.#Up & {
		ssh: {
			host: "143.198.64.230"
			user: "root"
		}
		source: repo
	}

	verify: docker.#Command & {
		ssh: up.run.ssh
		command: #"""
				docker container ls | grep "api" | grep "Up"
			"""#
	}

	cleanup: #CleanupCompose & {
		context: up.run
		ssh:     verify.ssh
	}
}
