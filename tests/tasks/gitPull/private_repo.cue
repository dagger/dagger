package main

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	inputs: secrets: token: command: {
		name: "sops"
		args: ["exec-env", "./secrets_sops.yaml", "echo $TestPAT"]
	}

	actions: {
		alpine: engine.#Pull & {
			source: "alpine:3.15.0"
		}

		testRepo: engine.#GitPull & {
			remote: "https://github.com/dagger/dagger.git"
			ref:    "main"
			auth: {
				username: "dagger-test"
				password: inputs.secrets.token.contents
			}
		}

		testContent: engine.#Exec & {
			input:  alpine.output
			always: true
			args: ["ls", "-l", "/repo"]
			mounts: inputRepo: {
				dest:     "/repo"
				contents: testRepo.output
			}
		}
	}
}
