package testing

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	inputs: {
		directories: testdata: path: "./testdata"
		secrets: dockerHubToken: command: {
			name: "sops"
			args: ["exec-env", "../../secrets_sops.yaml", "echo $DOCKERHUB_TOKEN"]
		}
	}

	actions: build: engine.#Build & {
		source: inputs.directories.testdata.contents
		auth: [{
			target:   "daggerio/ci-test:private-pull"
			username: "daggertest"
			secret:   inputs.secrets.dockerHubToken.contents
		}]
		dockerfile: contents: """
			FROM daggerio/ci-test:private-pull@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060
			"""
	}
}
