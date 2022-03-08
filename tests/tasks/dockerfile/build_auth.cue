package testing

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: {
		filesystem: testdata: read: contents: dagger.#FS
		commands: sops: {
			name: "sops"
			args: ["-d", "../../secrets_sops.yaml"]
			stdout: dagger.#Secret
		}
	}

	actions: {
		sopsSecrets: dagger.#DecodeSecret & {
			format: "yaml"
			input:  client.commands.sops.stdout
		}

		build: dagger.#Dockerfile & {
			source: client.filesystem.testdata.read.contents
			auth: "daggerio/ci-test:private-pull": {
				username: "daggertest"
				secret:   sopsSecrets.output.DOCKERHUB_TOKEN.contents
			}
			dockerfile: contents: """
				FROM daggerio/ci-test:private-pull@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060
				"""
		}
	}
}
