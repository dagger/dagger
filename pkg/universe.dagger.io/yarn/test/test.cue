package yarn

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/docker"
	"universe.dagger.io/yarn"
)

dagger.#Plan & {
	client: filesystem: {
		"./data/foo": read: contents: dagger.#FS
		"./data/bar": read: contents: dagger.#FS
	}

	actions: test: {

		// Configuration for all tests
		common: {
			data: client.filesystem."./data/foo".read.contents
		}

		// Run yarn.#Build
		simple: {
			build: yarn.#Build & {
				source: common.data
			}

			verify: #AssertFile & {
				input:    build.output
				path:     "test"
				contents: "output\n"
			}
		}

		// Run yarn.#Build with a custom name
		customName: {
			build: yarn.#Build & {
				name:   "My Build"
				source: common.data
			}
			verify: #AssertFile & {
				input:    build.output
				path:     "test"
				contents: "output\n"
			}
		}

		// Run yarn.#Build with a custom docker image
		customImage: {
			buildImage: docker.#Build & {
				steps: [
					docker.#Pull & {
						source: "alpine"
					},
					docker.#Run & {
						command: {
							name: "apk"
							args: ["add", "yarn", "bash"]
						}
					},
				]
			}

			image: build.output

			build: yarn.#Build & {
				source: common.data
				container: #input: buildImage.output
			}
		}
	}
}

// Make an assertion on the contents of a file
#AssertFile: {
	input:    dagger.#FS
	path:     string
	contents: string

	_read: core.#ReadFile & {
		"input": input
		"path":  path
	}

	actual: _read.contents

	// Assertion
	contents: actual
}
