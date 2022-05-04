package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: filesystem: {
		out_fs: write: contents:               actions.test.fs.data.output
		"out_files/test.txt": write: contents: actions.test.file.data.contents
		"out_files/secret.txt": write: {
			contents:    actions.test.secret.data.output
			permissions: 0o600
		}
		"out_files/execTest": write: contents: actions.test.exec.data.output
	}

	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		test: {
			fs: data: core.#WriteFile & {
				input:    dagger.#Scratch
				path:     "/test"
				contents: "foobar"
			}
			file: {
				// Only using contents for reference in client
				data: core.#WriteFile & {
					input:    dagger.#Scratch
					path:     "/test"
					contents: "foobaz"
				}
			}
			exec: {
				createOutput: core.#Exec & {
					args: ["sh", "-c", "echo 'hello world' | tee /output.txt"]
					input:  image.output
					always: true
				}
				data: core.#Copy & {
					input:    dagger.#Scratch
					contents: createOutput.output
					source:   "/output.txt"
				}
			}
			secret: {
				create: core.#WriteFile & {
					input:    dagger.#Scratch
					path:     "/test"
					contents: "foo-barab-oof"
				}
				data: core.#NewSecret & {
					input: create.output
					path:  "/test"
				}
			}
		}
	}
}
