package yarn

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/alpine"
	"alpha.dagger.io/os"
)

TestData: dagger.#Artifact

TestReact: {
	pkg: #Package & {
		source: TestData
	}

	test: os.#Container & {
		image: alpine.#Image & {
			package: bash: "=5.1.0-r0"
		}
		mount: "/build": from: pkg.build
		command: """
			test "$(cat /build/test)" = "output"
			"""
	}
}

TestData2: dagger.#Artifact

TestSecretsAndFile: {
	pkg: #Package & {
		source:       TestData2
		writeEnvFile: "/.env"
		env: {
			one: "one"
			two: "two"
		}
		secrets: {
			secretone: dagger.#Secret @dagger(input)
			secretwo:  dagger.#Secret @dagger(input)
		}
	}

	test: os.#Container & {
		image: alpine.#Image & {
			package: bash: "=5.1.0-r0"
		}
		shell: path: "/bin/bash"
		mount: "/build": from: pkg.build
		command: """
			content="$(cat /build/env)"
			[[ "${content}" = *"SECRETONE="* ]] && \\
			[[ "${content}" = *"SECRETWO="* ]] && \\
			[[ "${content}" = *"ONE=one"* ]] && \\
			[[ "${content}" = *"TWO=two"* ]]
			"""
	}
}
