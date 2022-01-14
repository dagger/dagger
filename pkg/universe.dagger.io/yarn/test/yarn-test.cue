package yarn

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"

	"universe.dagger.io/yarn"
	// "universe.dagger.io/alpine"
	// "universe.dagger.io/bash"
)

dagger.#Plan & {
	inputs: {
		directories: {
			testdata: path: "./testdata"
			testdata2: path: "./testdata2"
		}
	}

	actions: {
		TestReact: {
			cache: engine.#CacheDir & {
				id: "yarn cache"
			}

			pkg: yarn.#Build & {
				source: inputs.directories.testdata.contents
				"cache": cache
			}

			_image: engine.#Pull & {
            	source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
	        }

			// FIXME: use bash.#Script
			test: engine.#Exec & {
				input: _image.output
				mounts: build: {
					dest: "/build"
					contents: pkg.output
				}
				args: [
					"sh", "-c",
					#"""
					test "$(cat /build/test)" = "output"
					"""#
				]
			}
		}

		// FIXME: re-enable?
		// TestSecretsAndFile: {
		// 	pkg: #Package & {
		// 		source:       inputs.directories.testdata2
		// 		writeEnvFile: "/.env"
		// 		env: {
		// 			one: "one"
		// 			two: "two"
		// 		}
		// 		secrets: {
		// 			secretone: dagger.#Secret @dagger(input)
		// 			secretwo:  dagger.#Secret @dagger(input)
		// 		}
		// 	}

		// 	test: os.#Container & {
		// 		image: alpine.#Image & {
		// 			package: bash: true
		// 		}
		// 		shell: path: "/bin/bash"
		// 		mount: "/build": from: pkg.build
		// 		command: """
		// 			content="$(cat /build/env)"
		// 			[[ "${content}" = *"SECRETONE="* ]] && \\
		// 			[[ "${content}" = *"SECRETWO="* ]] && \\
		// 			[[ "${content}" = *"ONE=one"* ]] && \\
		// 			[[ "${content}" = *"TWO=two"* ]]
		// 			"""
		// 	}
		// }
	}
}