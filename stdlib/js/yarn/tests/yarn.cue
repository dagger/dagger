package yarn

import (
	"dagger.io/dagger"
	"dagger.io/alpine"
	"dagger.io/os"
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
