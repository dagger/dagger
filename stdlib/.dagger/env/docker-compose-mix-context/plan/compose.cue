package compose

import (
	"dagger.io/dagger"
	"dagger.io/docker"
	"dagger.io/docker/compose"
)

repo: dagger.#Artifact

TestCompose: {
	up: compose.#Up & {
		ssh: {
			host: "143.198.64.230"
			user: "root"
		}
		source: repo
		composeFile: #"""
			version: "3"

			services:
			  api-mix:
			    build: .
			    environment:
			      PORT: 7000
			    ports:
			    - 7000:7000

			networks:
			  default:
			    name: mix-context
			"""#
	}

	verify: docker.#Command & {
		ssh: up.run.ssh
		command: #"""
				docker container ls | grep "api-mix" | grep "Up"
			"""#
	}

	cleanup: #CleanupCompose & {
		context: up.run
		ssh:     verify.ssh
	}
}
