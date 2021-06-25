package compose

import (
	"alpha.dagger.io/docker"
	"alpha.dagger.io/docker/compose"
)

TestCompose: {
	up: compose.#Up & {
		ssh: {
			host: "143.198.64.230"
			user: "root"
		}
		composeFile: #"""
			version: "3"

			services:
			  nginx:
			    image: nginx:alpine
			    ports:
			    - 8000:80
			"""#
	}

	verify: docker.#Command & {
		ssh: up.run.ssh
		command: #"""
				docker container ls | grep "nginx" | grep "Up"
			"""#
	}

	// Can't simply use docker.#Command because we need to keep docker-compose context
	cleanup: #CleanupCompose & {
		context: up.run
		ssh:     verify.ssh
	}
}
