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
			wptest: go.#Test & {
				source:  _src
				package: "./greeting"
			}

			verify: docker.#Run & {
				input:  wptest.output
				always: true
				command: {
					name: "sh"
					args: [ "-e", "-c", """
						echo "========== START"
						find ~/test/
						echo "========== DONE"
						test "OK" = $(cat ~/test/test-greeting-*/greeting_test.result)
						test ! -f "~/test/test-math-*/math_test.result"
						""",
					]
				}
			}
		}

		withPackages: {
			wpstest: go.#Test & {
				source: _src
				packages: ["./greeting", "./math"]
			}

			verify: docker.#Run & {
				input:  wpstest.output
				always: true
				command: {
					name: "sh"
					args: [ "-e", "-c", """
						echo "========== START"
						find ~/test/
						echo "========== DONE"
						test "OK" = $(cat ~/test/test-greeting-*/greeting_test.result)
						test "OK" = $(cat ~/test/test-math-*/math_test.result)
						""",
					]
				}
			}
		}

		withBoth: {
			wbtest: go.#Test & {
				source:  _src
				package: "./greeting"
				packages: ["./math"]
			}

			verify: docker.#Run & {
				input:  wbtest.output
				always: true
				command: {
					name: "sh"
					args: [ "-e", "-c", """
						echo "========== START"
						find ~/test/
						echo "========== DONE"
						# when *packages* is set, *package* will be ignored. *math* will be selected'
						test "OK" = $(cat ~/test/test-math-*/math_test.result)
						test ! -f "~/test/test-greeting-*/greeting_test.result"
						""",
					]
				}
			}
		}
	}
}
