package main

import (
	"strings"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: commands: sops: {
		name: "sops"
		args: ["-d", "secrets_sops.yaml"]
		stdout: dagger.#Secret
	}

	#auth: {
		username: "daggertest"
		secret:   actions.sopsSecrets.output.DOCKERHUB_TOKEN.contents
	}

	actions: {
		sopsSecrets: core.#DecodeSecret & {
			format: "yaml"
			input:  client.commands.sops.stdout
		}

		randomString: {
			baseImage: core.#Pull & {
				source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
			}

			image: core.#Exec & {
				input: baseImage.output
				args: [
					"sh", "-c", "echo -n $RANDOM > /output.txt",
				]
			}

			outputFile: core.#ReadFile & {
				input: image.output
				path:  "/output.txt"
			}

			output: outputFile.contents
		}

		// Push image with random content
		push: core.#Push & {
			dest:  "daggerio/ci-test:\(randomString.output)"
			input: randomString.image.output
			config: env: FOO: randomString.output
			auth: #auth
		}

		// Pull same image and check the content
		pull: core.#Pull & {
			source: "daggerio/ci-test:\(randomString.output)"
			auth:   #auth
		} & {
			// check digest
			digest: strings.Split(push.result, "@")[1]
			// check image config
			config: env: FOO: randomString.output
		}

		pullOutputFile: core.#ReadFile & {
			input: pull.output
			path:  "/output.txt"
		}

		// Check output file in the pulled image
		pullContent: string & pullOutputFile.contents & randomString.contents
	}
}
