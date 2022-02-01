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
		sopsSecrets: dagger.#DecodeSecret & {
			format: "yaml"
			input:  inputs.secrets.sops.contents
		}

		pull: engine.#Pull & {
			source: "daggerio/ci-test:private-pull@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060"
			auth: {
				username: "daggertest"
				secret:   sopsSecrets.output.DOCKERHUB_TOKEN.contents
			}
		} & {
			// assert result
			digest: "sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060"
			config: {
				env: PATH: "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
				cmd: ["/bin/sh"]
			}
		}
	}
}
