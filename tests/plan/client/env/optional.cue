package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	client: env: {
		TEST_DEFAULT:   string | *"hello world"
		TEST_OPTIONAL?: dagger.#Secret
	}
	actions: {
		image: core.#Pull & {
			source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
		}
		test: {
			set: {
				default: core.#Exec & {
					input: image.output
					args: ["test", client.env.TEST_DEFAULT, "=", "hello universe"]
				}
				optional: {
					file: core.#Exec & {
						input: image.output
						mounts: {
							if client.env.TEST_OPTIONAL != _|_ {
								secret: {
									type:     "secret"
									contents: client.env.TEST_OPTIONAL
									dest:     "/secret"
								}
							}
						}
						args: ["sh", "-c", "test $(cat /secret) = foobar"]
					}
					env: core.#Exec & {
						input: image.output
						if client.env.TEST_OPTIONAL != _|_ {
							env: TEST_OPTIONAL: client.env.TEST_OPTIONAL
						}
						args: ["sh", "-c", "test $TEST_OPTIONAL = foobar"]
					}
				}
			}
			unset: {
				default: core.#Exec & {
					input: image.output
					args: ["test", client.env.TEST_DEFAULT, "=", "hello world"]
				}
				optional: core.#Exec & {
					input: image.output
					mounts: {
						if client.env.TEST_OPTIONAL != _|_ {
							secret: {
								contents: client.env.TEST_OPTIONAL
								dest:     "/secret"
							}
						}
					}
					args: ["test", "[ ! -f /secret ]"]
				}
			}
		}
	}
}
