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
	// Generate a random string.
	// Seed is used to force buildkit execution and not simply use a previous generated string.
	suffix: random.#String & {seed: "cmp"}

	name: "compose_test_\(suffix.out)"

	app: #App & {
		ssh: {
			key:  TestSSH.key
			host: TestSSH.host
			user: TestSSH.user
		}
		source: repo
		"name": name
	}

	test: docker.#Command & {
		ssh:     app.deployment.ssh
		command: #"""
				docker container ls | grep "\#(name)_api" | grep "Up"
			"""#
	}

	cleanup: #CleanupCompose & {
		context: app.deployment
		"name":  name
		ssh:     test.ssh
	}
}

TestInlineCompose: {
	// Generate a random string.
	// Seed is used to force buildkit execution and not simply use a previous generated string.
	suffix: random.#String & {seed: "cmp-inline"}

	name: "inline_test_\(suffix.out)"

	app: #App & {
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
			    - 7000
			"""#
	}

	test: docker.#Command & {
		ssh:     app.deployment.ssh
		command: #"""
				docker container ls | grep "\#(name)_api-mix" | grep "Up"
			"""#
	}

	cleanup: #CleanupCompose & {
		context: app.deployment
		"name":  name
		ssh:     test.ssh
	}
}
