package testing

import (
	"dagger.io/dagger/engine"
	"encoding/yaml"
)

engine.#Plan & {
	inputs: {
		directories: testdata: path: "./testdata"
		secrets: sops: command: {
			name: "sops"
			args: ["-d", "../../secrets_sops.yaml"]
		}
	}

	actions: {
		dockerHubToken: engine.#TransformSecret & {
			input: inputs.secrets.sops.contents
			#function: {
				input:  _
				output: yaml.Unmarshal(input).DOCKERHUB_TOKEN
			}
		}

		build: engine.#Build & {
			source: inputs.directories.testdata.contents
			auth: [{
				target:   "daggerio/ci-test:private-pull"
				username: "daggertest"

				secret: dockerHubToken.output
			}]
			dockerfile: contents: """
				FROM daggerio/ci-test:private-pull@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060
				"""
		}
	}
}
