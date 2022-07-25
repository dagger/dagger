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
				input:  test.output
				always: true
				command: {
					name: "sh"
					args: [ "-e", "-c", """
						echo "========== START"
						find /tmp/
						echo "========== DONE"
						test "OK" = $(cat /tmp/test-greeting-*/greeting_test.result)
						test ! -f "/tmp/test-math-*/math_test.result"
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
				input:  test.output
				always: true
				command: {
					name: "sh"
					args: [ "-e", "-c", """
						echo "========== START"
						find /tmp/

						echo "========== DONE"
						test "OK" = $(cat /tmp/test-greeting-*/greeting_test.result)
						test "OK" = $(cat /tmp/test-math-*/math_test.result)
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
				input:  test.output
				always: true
				command: {
					name: "sh"
					args: [ "-e", "-c", """
						echo "========== START"
						find /tmp/
						echo "========== DONE"
						# when *packages* is set, *package* will be ignored. *math* will be selected'
						test "OK" = $(cat /tmp/test-math-*/math_test.result)
						test ! -f "/tmp/test-greeting-*/greeting_test.result"
						""",
					]
				}
			}
		}
	}
}
