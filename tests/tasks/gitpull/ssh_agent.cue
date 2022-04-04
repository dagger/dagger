package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: {
		env: SSH_AUTH_SOCK: string

		network: sshAgent: {
			address: "unix://\(client.env.SSH_AUTH_SOCK)"
			connect: dagger.#Socket
		}
	}

	actions: test: {
		pull: core.#GitPull & {
			remote: "git@github.com:dagger/test.git"
			ref:    "main"
			auth: sshAgent: client.network.sshAgent.connect
		}

		_image: core.#Pull & {
			source: "alpine:3.15.0"
		}

		verify: core.#Exec & {
			input:  _image.output
			always: true
			args: ["ls", "-l", "/repo/README.md"]
			mounts: inputRepo: {
				dest:     "/repo"
				contents: pull.output
			}
		}
	}
}
