package go

import (
	"dagger.io/dagger"
	"dagger.io/go"
	"dagger.io/alpine"
	"dagger.io/llb"
)

TestData: dagger.#Artifact

TestGoBuild: {
	build: go.#Build & {
		source: TestData
		output: "/bin/testbin"
	}

	test: #compute: [
		llb.#Load & {from: alpine.#Image},
		llb.#Exec & {
			args: [
				"sh",
				"-ec",
				"""
					test "$(/bin/testbin)" = "hello world"
					""",
			]
			mount: "/bin/testbin": {
				from: build
				path: "/bin/testbin"
			}
		},
	]
}
