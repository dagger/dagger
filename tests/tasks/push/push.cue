package main

import (
	"strings"
	"alpha.dagger.io/europa/dagger/engine"
)

engine.#Plan & {
	inputs: secrets: dockerHubToken: command: {
		name: "sops"
		args: ["exec-env", "./secrets_sops.yaml", "echo $DOCKERHUB_TOKEN"]
	}

	#auth: [{
		target:   "daggerio/ci-test:private-pull"
		username: "daggertest"
		secret:   inputs.secrets.dockerHubToken.contents
	}]

	actions: {
		randomString: {
			baseImage: engine.#Pull & {
				source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
			}

			image: engine.#Exec & {
				input: baseImage.output
				args: [
					"sh", "-c", "echo -n $RANDOM > /output.txt",
				]
			}

			outputFile: engine.#ReadFile & {
				input: image.output
				path:  "/output.txt"
			}

			output: outputFile.contents
		}

		// Push image with random content
		push: engine.#Push & {
			dest:  "daggerio/ci-test:\(randomString.output)"
			input: randomString.image.output
			config: Env: ["FOO=\(randomString.output)"]
			auth: #auth
		}

		// Pull same image and check the content
		pull: engine.#Pull & {
			source: "daggerio/ci-test:\(randomString.output)"
			auth:   #auth
		} & {
			// check digest
			digest: strings.Split(push.result, "@")[1]
			// check image config
			config: {
				Env: ["FOO=\(randomString.output)"]
			}
		}

		pullOutputFile: engine.#ReadFile & {
			input: pull.output
			path:  "/output.txt"
		}

		// Check output file in the pulled image
		pullContent: string & pullOutputFile.contents & randomString.contents
	}
}
