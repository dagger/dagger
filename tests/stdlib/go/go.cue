package go

import (
	"dagger.io/dagger"
	"dagger.io/go"
	"dagger.io/alpine"
)

TestData: dagger.#Artifact

TestGoBuild: {
	build: go.#Build & {
		source: TestData
		output: "/bin/testbin"
	}

	test: #compute: [
		dagger.#Load & {from: alpine.#Image},
		dagger.#Exec & {
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
