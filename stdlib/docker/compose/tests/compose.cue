package compose

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/random"
)

repo: dagger.#Artifact @dagger(input)

TestSSH: {
	key:  dagger.#Secret @dagger(input)
	host: string         @dagger(input)
	user: string         @dagger(input)
}

TestCompose: {
	suffix: random.#String & {seed: "cmp"}

	name: "compose_test_\(suffix.out)"

	up: #App & {
		ssh: {
			key:  TestSSH.key
			host: TestSSH.host
			user: TestSSH.user
		}
		source: repo
		"name": name
	}

	verify: docker.#Command & {
		ssh:     up.run.ssh
		command: #"""
				docker container ls | grep "\#(name)_api" | grep "Up"
			"""#
	}

	cleanup: #CleanupCompose & {
		context: up.run
		"name":  name
		ssh:     verify.ssh
	}
}

TestInlineCompose: {
	suffix: random.#String & {seed: "cmp-inline"}

	name: "inline_test_\(suffix.out)"

	up: #App & {
		ssh: {
			key:  TestSSH.key
			host: TestSSH.host
			user: TestSSH.user
		}
		source: repo
		"name": name
		composeFile: #"""
			version: "3"

			services:
			  api-mix:
			    build: .
			    environment:
			      PORT: 7000
			    ports:
			    - 7000:7000
			"""#
	}

	verify: docker.#Command & {
		ssh:     up.run.ssh
		command: #"""
				docker container ls | grep "\#(name)_api-mix" | grep "Up"
			"""#
	}

	cleanup: #CleanupCompose & {
		context: up.run
		"name":  name
		ssh:     verify.ssh
	}
}
