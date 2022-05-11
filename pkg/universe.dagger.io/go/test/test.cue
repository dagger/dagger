package go

import (
	"dagger.io/dagger"

	"universe.dagger.io/docker"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	client: filesystem: "./data/hello": read: contents: dagger.#FS

	actions: test: {
		_src: client.filesystem."./data/hello".read.contents

		simple: go.#Test & {
			source: _src
		}

		withPackage: {
			test: go.#Test & {
				source:  _src
				package: "./greeting"
			}

			verify: docker.#Run & {
				input: test.output
				command: {
					name: "sh"
					args: [ "-c", """
						test "OK" = $(cat /tmp/greeting_test.result)
						test ! -f "/tmp/math_test.result"
						""",
					]
				}
			}
		}

		withPackages: {
			test: go.#Test & {
				source: _src
				packages: ["./greeting", "./math"]
			}

			verify: docker.#Run & {
				input: test.output
				command: {
					name: "sh"
					args: [ "-c", """
						test "OK" = $(cat /tmp/greeting_test.result)
						test "OK" = $(cat /tmp/math_test.result)
						""",
					]
				}
			}
		}

		withBoth: {
			test: go.#Test & {
				source:  _src
				package: "./greeting"
				packages: ["./math"]
			}

			verify: docker.#Run & {
				input: test.output
				command: {
					name: "sh"
					args: [ "-c", """
						test "OK" = $(cat /tmp/greeting_test.result)
						test "OK" = $(cat /tmp/math_test.result)
						""",
					]
				}
			}
		}
	}
}
