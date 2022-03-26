package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: commands: sops: {
		name: "sops"
		args: ["-d", "secrets_sops.yaml"]
		stdout: dagger.#Secret
	}

	actions: {

		alpine: core.#Pull & {
			source: "alpine:3.15.0"
		}

		sopsSecrets: core.#DecodeSecret & {
			format: "yaml"
			input:  client.commands.sops.stdout
		}

		testRepo: core.#GitPull & {
			remote: "https://github.com/dagger/dagger.git"
			ref:    "main"
			auth: {
				username: "dagger-test"
				password: sopsSecrets.output.TestPAT.contents
			}
		}

		testContent: core.#Exec & {
			input:  alpine.output
			always: true
			args: ["ls", "-l", "/repo/README.md"]
			mounts: inputRepo: {
				dest:     "/repo"
				contents: testRepo.output
			}
		}
	}
}
