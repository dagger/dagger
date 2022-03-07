package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: {
		out_fs: write: contents:               actions.test.fs.data.output
		"out_files/test.txt": write: contents: actions.test.file.data.contents
		"out_files/secret.txt": write: {
			contents:    actions.test.secret.data.output
			permissions: 0o600
		}
	}

	actions: {
		image: dagger.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		test: {
			fs: {
				data: dagger.#WriteFile & {
					input:    dagger.#Scratch
					path:     "/test"
					contents: "foobar"
				}
				// FIXME: hack until we can do outputs with `dagger do`
				verify: dagger.#ReadFile & {
					input: client.filesystem."out_fs".write.contents
					path:  "test"
				}
			}
			file: {
				// Only using contents for reference in client
				data: dagger.#WriteFile & {
					input:    dagger.#Scratch
					path:     "/test"
					contents: "foobaz"
				}
				// FIXME: hack until we can do outputs with `dagger do`
				verify: dagger.#Exec & {
					input: image.output
					args: ["echo", "-c", client.filesystem."out_files/test.txt".write.contents]
				}
			}
			secret: {
				create: dagger.#WriteFile & {
					input:    dagger.#Scratch
					path:     "/test"
					contents: "foo-barab-oof"
				}
				data: dagger.#NewSecret & {
					input: create.output
					path:  "/test"
				}
				// FIXME: hack until we can do outputs with `dagger do`
				verify: dagger.#Exec & {
					input: image.output
					mounts: secret: {
						dest:     "/run/secrets/test"
						contents: client.filesystem."out_files/secret.txt".write.contents
					}
					args: ["id"]
				}
			}
		}
	}
}
