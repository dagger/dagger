package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	inputs: secrets: sops: command: {
		name: "sops"
		args: ["-d", "../../secrets_sops.yaml"]
	}

	actions: {

		alpine: engine.#Pull & {
			source: "alpine:3.15.0"
		}

		sopsSecrets: dagger.#DecodeSecret & {
			format: "yaml"
			input:  inputs.secrets.sops.contents
		}

		testRepo: engine.#GitPull & {
			remote: "https://github.com/dagger/dagger.git"
			ref:    "main"
			auth: {
				username: "dagger-test"
				password: sopsSecrets.output.TestPAT.contents
			}
		}

		testContent: engine.#Exec & {
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
