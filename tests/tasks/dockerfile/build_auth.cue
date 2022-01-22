package testing

import (
	"encoding/yaml"

	"dagger.io/dagger/engine"
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
		secrets: engine.#TransformSecret & {
			input: inputs.secrets.sops.contents
			#function: {
				input:  _
				output: yaml.Unmarshal(input)
			}
		}

		build: engine.#Build & {
			source: inputs.directories.testdata.contents
			auth: [{
				target:   "daggerio/ci-test:private-pull"
				username: "daggertest"

				secret: secrets.output.dockerhub.token.contents
			}]
			dockerfile: contents: """
				FROM daggerio/ci-test:private-pull@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060
				"""
		}
	}
}
