package yarn

import (
	"dagger.io/dagger"
	"dagger.io/yarn"
	"dagger.io/alpine"
	"dagger.io/llb"
)

TestData: dagger.#Artifact

TestYarn: {
	run: yarn.#Script & {
		source: TestData
	}

	test: #compute: [
		llb.#Load & {from: alpine.#Image & {
			package: bash: "=5.1.0-r0"
		}},
		llb.#Exec & {
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
