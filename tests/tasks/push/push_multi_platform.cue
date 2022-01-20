package main

import (
	"dagger.io/dagger/engine"
)

engine.#Plan & {
	inputs: secrets: DOCKERHUB_TOKEN: command: {
		name: "sops"
		args: ["exec-env", "../../secrets_sops.yaml", "echo $DOCKERHUB_TOKEN"]
	}

	#target: "daggerio/ci-test:multi-platform"

	#auth: [{
		target:   #target
		username: "daggertest"
		secret:   inputs.secrets.DOCKERHUB_TOKEN.contents
	}]

	#platforms: ["linux/amd64", "linux/arm64"]

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

		for p in #platforms {
			"image-\(p)": engine.#Dockerfile & {
				source: engine.#Scratch
				dockerfile: contents: #"""
					FROM alpine

					RUN uname -m > /arch.txt
					"""#
				platform: p
			}
		}

		push: engine.#Push & {
			dest: "\(#target)-\(randomString.output)"
			inputs: {
				for p in #platforms {
					"\(p)": input: actions["image-\(p)"].output
				}
			}
			auth: #auth
		}

		"test-linux-arm64": {
			image: engine.#Pull & {
				source:   push.result
				platform: "linux/arm64"
			}

			test: engine.#ReadFile & {
				input: image.output
				path:  "/arch.txt"
			} & {
				contents: "aarch64\n"
			}
		}

		"test-linux-amd64": {
			image: engine.#Pull & {
				source:   push.result
				platform: "linux/amd64"
			}

			test: engine.#ReadFile & {
				input: image.output
				path:  "/arch.txt"
			} & {
				contents: "x86_64\n"
			}
		}
	}
}
