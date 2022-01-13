package main

import (
	"encoding/yaml"
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

		repoPassword: engine.#TransformSecret & {
			input: inputs.secrets.sops.contents
			#function: {
				input:  _
				output: yaml.Unmarshal(input)
			}
		}

		testRepo: engine.#GitPull & {
			remote: "https://github.com/dagger/dagger.git"
			ref:    "main"
			auth: {
				username: "dagger-test"
				password: repoPassword.output.TestPAT.contents
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
