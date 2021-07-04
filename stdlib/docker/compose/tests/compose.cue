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

	up: #App & {
		sshConfig: {
			key:  TestSSH.key
			host: TestSSH.host
			user: TestSSH.user
		}
		source: repo
		"name": name
	}

	verify: docker.#Command & {
		sshConfig: up.run.sshConfig
		command:   #"""
	   docker container ls | grep "\#(name)_api" | grep "Up"
	  """#
	}

	cleanup: #CleanupCompose & {
		context:   up.run
		"name":    name
		sshConfig: verify.sshConfig
	}
}

TestInlineCompose: {
	// Generate a random string.
	// Seed is used to force buildkit execution and not simply use a previous generated string.
	suffix: random.#String & {seed: "cmp-inline"}

	name: "inline_test_\(suffix.out)"

	up: #App & {
		sshConfig: {
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

	verify: docker.#Command & {
		sshConfig: up.run.sshConfig
		command:   #"""
				docker container ls | grep "\#(name)_api-mix" | grep "Up"
			"""#
	}

	cleanup: #CleanupCompose & {
		context:   up.run
		"name":    name
		sshConfig: verify.sshConfig
	}
}

TestComposeVolume: {
	// Generate a random string.
	// Seed is used to force buildkit execution and not simply use a previous generated string.
	suffix: random.#String & {seed: "cmp-inline"}

	name: "mount_test_\(suffix.out)"

	artifact: dagger.#Artifact & dagger.#Input

	up: #App & {
		sshConfig: {
			key:  TestSSH.key
			host: TestSSH.host
			user: TestSSH.user
		}
		"name": name
		composeFile: #"""
			  version: "3"

			  services:
			    nginx:
			      image: nginx:latest
			      volumes:
			      - $HOME/data:/usr/share/nginx/html
			      - $HOME/data:/foo
			      ports:
			      - 80
			"""#
		volumes: "/root/data": from: artifact
	}

	verify: docker.#Command & {
		sshConfig: up.run.sshConfig
		command:   #"""
					docker container ls | grep "\#(name)_nginx" | grep "Up"
				"""#
	}

	cleanup: #CleanupCompose & {
		context:   up.run
		"name":    name
		sshConfig: verify.sshConfig
	}
}
