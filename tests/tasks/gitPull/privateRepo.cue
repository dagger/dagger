package main

import "alpha.dagger.io/europa/dagger/engine"

engine.#Plan & {
	inputs: secrets: TestPAT: command: {
		name: "sops"
		args: ["exec-env", "./privateRepo.enc.yaml", "echo $data"]
	}
	actions: {
		alpine: engine.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}

		testRepo: engine.#GitPull & {
			remote:    "https://github.com/dagger/dagger.git"
			ref:       "main"
			authToken: inputs.secrets.TestPAT.contents
		}

		testContent: engine.#Exec & {
			input:  alpine.output
			always: true
			args: ["ls", "-l", "/input/repo | grep 'universe -> stdlib'"]
			mounts: inputRepo: {
				dest:     "/input/repo"
				contents: testRepo.output
			}
		}
	}
}
