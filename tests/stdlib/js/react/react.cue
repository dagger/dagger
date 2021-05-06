package react

import (
	"dagger.io/dagger"
	"dagger.io/js/react"
	"dagger.io/alpine"
	"dagger.io/os"
)

TestData: dagger.#Artifact

TestReact: {
	app: react.#App & {
		source: TestData
	}

	test: os.#Container & {
		image: alpine.#Image & {
			package: bash: "=5.1.0-r0"
		}
		mount: "/build": from: app.build
		command: """
			test "$(cat /build/test)" = "output"
			"""
	}
}
