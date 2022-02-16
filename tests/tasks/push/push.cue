package main

import (
	"strings"
	"dagger.io/dagger"
)

dagger.#Plan & {
	inputs: secrets: sops: command: {
		name: "sops"
		args: ["-d", "../../secrets_sops.yaml"]
	}

	#auth: {
		username: "daggertest"
		secret:   actions.sopsSecrets.output.DOCKERHUB_TOKEN.contents
	}

	actions: {

		sopsSecrets: dagger.#DecodeSecret & {
			format: "yaml"
			input:  inputs.secrets.sops.contents
		}

		randomString: {
			baseImage: dagger.#Pull & {
				source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
			}

			image: dagger.#Exec & {
				input: baseImage.output
				args: [
					"sh", "-c", "echo -n $RANDOM > /output.txt",
				]
			}

			outputFile: dagger.#ReadFile & {
				input: image.output
				path:  "/output.txt"
			}

			output: outputFile.contents
		}

		// Push image with random content
		push: dagger.#Push & {
			dest:  "daggerio/ci-test:\(randomString.output)"
			input: randomString.image.output
			config: env: FOO: randomString.output
			auth: #auth
		}

		// Pull same image and check the content
		pull: dagger.#Pull & {
			source: "daggerio/ci-test:\(randomString.output)"
			auth:   #auth
		} & {
			// check digest
			digest: strings.Split(push.result, "@")[1]
			// check image config
			config: env: FOO: randomString.output
		}

		pullOutputFile: dagger.#ReadFile & {
			input: pull.output
			path:  "/output.txt"
		}

		// Check output file in the pulled image
		pullContent: string & pullOutputFile.contents & randomString.contents
	}
}
