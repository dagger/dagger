package go

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
	"universe.dagger.io/go"
)

dagger.#Plan & {
	client: filesystem: "./data/hello": read: contents: dagger.#FS

	actions: test: {
		_baseImage: alpine.#Build
		_src:       client.filesystem."./data/hello".read.contents

		simple: go.#Test & {
			source: _src
		}

		withPackage: {
			test: go.#Test & {
				source:  client.filesystem."./data/hello".read.contents
				package: "./greeting"
			}
			verify: docker.#Run & {
				input: _baseImage.output
				command: {
					name: "sh"
					args: [ "-c", """
						test "OK" = "`cat /src/greeting/greeting_test.result`"
						test -f "/src/math/math_test.result"
						""",
					]
				}

				mounts: src: {
					contents: _src
					source:   "/"
					dest:     "/src"
				}
			}
		}

		withPackages: {
			test: go.#Test & {
				source: client.filesystem."./data/hello".read.contents
				packages: ["./greeting", "./math"]
			}
			verify: docker.#Run & {
				input: _baseImage.output
				command: {
					name: "sh"
					args: [ "-c", """
						test "OK" = "`cat /src/greeting/greeting_test.result`"
						test "OK" = "`cat /src/math/math_test.result`"
						""",
					]
				}

				mounts: src: {
					contents: _src
					source:   "/"
					dest:     "/src"
				}
			}
		}

		withBoth: {
			test: go.#Test & {
				source:  client.filesystem."./data/hello".read.contents
				package: "./greeting"
				packages: ["./math"]
			}
			verify: docker.#Run & {
				input: _baseImage.output
				command: {
					name: "sh"
					args: [ "-c", """
						test "OK" = "`cat /src/greeting/greeting_test.result`"
						test "OK" = "`cat /src/math/math_test.result`"
						""",
					]
				}

				mounts: src: {
					contents: _src
					source:   "/"
					dest:     "/src"
				}
			}
		}
	}
}
