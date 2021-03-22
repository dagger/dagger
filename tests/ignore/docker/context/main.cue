package test

import (
	"dagger.io/alpine"
	"dagger.io/dagger"
	"dagger.io/llb"
)

TestData: dagger.#Artifact

TestIgnore: {
	string
	#compute: [
		llb.#Load & {from: alpine.#Image},
		llb.#Exec & {
			args: ["sh", "-c", "ls -a /src"]
			mount: "/src": from: TestData
		},
		llb.#DockerBuild & {
			context: TestData
		},
	]
}

