package compose

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
)

repo: dagger.#Artifact @dagger(input)

TestSSH: {
	key:  dagger.#Secret @dagger(input)
	host: string         @dagger(input)
	user: string         @dagger(input)
}

TestCompose: {
	up: #App & {
		ssh: {
			key:  TestSSH.key
			host: TestSSH.host
			user: TestSSH.user
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

TestInlineCompose: {
	up: #App & {
		ssh: {
			key:  TestSSH.key
			host: TestSSH.host
			user: TestSSH.user
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
