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
		secrets: engine.#TransformSecret & {
			input: inputs.secrets.sops.contents
			#function: {
				input:  _
				output: yaml.Unmarshal(input)
			}
		}

		pull: engine.#Pull & {
			source: "daggerio/ci-test:private-pull@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060"
			auth: [{
				target:   "daggerio/ci-test:private-pull"
				username: "daggertest"
				secret:   secrets.output.dockerhub.token.contents
			}]
		} & {
			// assert result
			digest: "sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060"
			config: {
				Env: ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"]
				Cmd: ["/bin/sh"]
			}
		}
	}
}
