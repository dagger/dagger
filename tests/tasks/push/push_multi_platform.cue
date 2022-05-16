package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

#RandomString: {
	_baseImage: core.#Pull & {
		source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
	}

	_image: core.#Exec & {
		input: _baseImage.output
		args: [
			"sh", "-c", "echo -n $RANDOM > /output.txt",
		]
	}

	_outputFile: core.#ReadFile & {
		input: _image.output
		path:  "/output.txt"
	}

	output: _outputFile.contents
}

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

	// Platforms lists
	#platforms: {
		"linux/amd64": _
		"linux/arm64": _
		"linux/s390x": _
	}

	// Platforms names
	// Avoid future duplication and ensure that all `#platforms` are named
	#names: #platforms & close({
		"linux/amd64": "x86_64"
		"linux/arm64": "aarch64"
		"linux/s390x": "s390x"
	})

	actions: {
		sopsSecrets: core.#DecodeSecret & {
			format: "yaml"
			input:  client.commands.sops.stdout
		}

		_randomString: #RandomString & {}

		build: {
			#platforms

			[p=string]: core.#Dockerfile & {
				source: dagger.#Scratch
				dockerfile: contents: #"""
					FROM alpine

					RUN uname -m | tr -d '\n' > /arch.txt
					"""#
				platform: p
			}
		}

		push: core.#Push & {
			dest: "\(#target)-\(_randomString.output)"
			inputs: {
				for p, b in actions.build {
					"\(p)": input: b.output
				}
			}
			auth: #auth
		}

		test: {
			#platforms

			[p=string]: {
				image: core.#Pull & {
					source:   actions.push.result
					platform: p
				}

				verify: core.#ReadFile & {
					input: image.output
					path:  "/arch.txt"
				} & {
					contents: #names[p]
				}
			}
		}
	}
}
