package main

import (
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

	#target: "daggerio/ci-test:multi-platform"

	#platforms: ["linux/amd64", "linux/arm64"]

	actions: {
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

		for p in #platforms {
			"image-\(p)": core.#Dockerfile & {
				source: dagger.#Scratch
				dockerfile: contents: #"""
					FROM alpine

					RUN uname -m > /arch.txt
					"""#
				platform: p
			}
		}

		push: core.#Push & {
			dest: "\(#target)-\(randomString.output)"
			inputs: {
				for p in #platforms {
					"\(p)": input: actions["image-\(p)"].output
				}
			}
			auth: #auth
		}

		"test-linux-arm64": {
			image: core.#Pull & {
				source:   push.result
				platform: "linux/arm64"
			}

			test: core.#ReadFile & {
				input: image.output
				path:  "/arch.txt"
			} & {
				contents: "aarch64\n"
			}
		}

		"test-linux-amd64": {
			image: core.#Pull & {
				source:   push.result
				platform: "linux/amd64"
			}

			test: core.#ReadFile & {
				input: image.output
				path:  "/arch.txt"
			} & {
				contents: "x86_64\n"
			}
		}
	}
}
