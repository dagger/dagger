package yarn

import (
	"dagger.io/dagger"
	"dagger.io/yarn"
	"dagger.io/alpine"
)

TestData: dagger.#Dir

TestYarn: {
	run: yarn.#Script & {
		source: TestData
	}

	test: #compute: [
		dagger.#Load & {from: alpine.#Image & {
			package: bash: "=5.1.0-r0"
		}},
		dagger.#Exec & {
			mount: "/build": from: run
			args: [
				"/bin/bash",
				"--noprofile",
				"--norc",
				"-eo",
				"pipefail",
				"-c",
				"""
					test "$(cat /build/test)" = "output"
					""",
			]
		},
	]
}
