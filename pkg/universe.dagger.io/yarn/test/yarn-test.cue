package yarn

import (
	"dagger.io/dagger"
	"universe.dagger.io/yarn"
	"universe.dagger.io/alpine"
	"universe.dagger.io/bash"
)

dagger.#Plan & {
	inputs: directories: {
		testdata: path:  "./testdata"
		testdata2: path: "./testdata2"
	}

	actions: {
		cache: dagger.#CacheDir & {
			id: "yarn cache"
		}

		pkg: yarn.#Build & {
			source:  inputs.directories.testdata.contents
			"cache": cache
		}

		_image: alpine.#Build & {
			packages: bash: {}
		}

		test: bash.#Run & {
			image: _image.output
			mounts: build: {
				dest:     "/build"
				contents: pkg.output
			}
			script: #"""
				test "$(cat /build/test)" = "output"
				"""#
		}

		// FIXME: re-enable?
		// TestSecretsAndFile: {
		//  pkg: #Package & {
		//   source:       inputs.directories.testdata2
		//   writeEnvFile: "/.env"
		//   env: {
		//    one: "one"
		//    two: "two"
		//   }
		//   secrets: {
		//    secretone: dagger.#Secret @dagger(input)
		//    secretwo:  dagger.#Secret @dagger(input)
		//   }
		//  }

		//  test: os.#Container & {
		//   image: alpine.#Image & {
		//    package: bash: true
		//   }
		//   shell: path: "/bin/bash"
		//   mount: "/build": from: pkg.build
		//   command: """
		//    content="$(cat /build/env)"
		//    [[ "${content}" = *"SECRETONE="* ]] && \\
		//    [[ "${content}" = *"SECRETWO="* ]] && \\
		//    [[ "${content}" = *"ONE=one"* ]] && \\
		//    [[ "${content}" = *"TWO=two"* ]]
		//    """
		//  }
		// }
	}
}
