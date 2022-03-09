package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: commands: sops: {
		name: "sops"
		args: ["-d", "secrets_sops.yaml"]
		stdout: dagger.#Secret
	}

	actions: {
		sopsSecrets: dagger.#DecodeSecret & {
			format: "yaml"
			input:  client.commands.sops.stdout
		}

		pull: dagger.#Pull & {
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
