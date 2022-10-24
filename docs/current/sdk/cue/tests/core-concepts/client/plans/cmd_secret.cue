package main

import (
	"strings"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: {
		env: PRIVATE_KEY: string | *"/home/user/.ssh/id_rsa"
		commands: {
			pkey: {
				name: "cat"
				args: [env.PRIVATE_KEY]
				stdout: dagger.#Secret
			}
			digest: {
				name: "openssl"
				args: ["dgst", "-sha256"]
				stdin: pkey.stdout // a secret
			}
		}
	}

	actions: test: {
		_op: core.#Nop & {
			input: strings.TrimSpace(client.commands.digest.stdout)
		}
		digest: _op.output
	}
}
