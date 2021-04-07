package react

import (
	"dagger.io/dagger"
	"dagger.io/js/react"
	"dagger.io/alpine"
	"dagger.io/docker"
)

TestData: dagger.#Artifact

TestReact: {
	app: react.#App & {
		source: TestData
	}

	test: docker.#Container & {
		image: alpine.#Image & {
			package: bash: "=5.1.0-r0"
		}
		volume: build: {
			from: app.build
			dest: "/build"
		}
		command: """
			test "$(cat /build/test)" = "output"
			"""
		shell: {
			path: "/bin/bash"
			args: [
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
			]
		}
	}
}
