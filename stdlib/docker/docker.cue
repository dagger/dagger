package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#Image: dagger.#Artifact

#Ref: string

// Build a docker container image
#Build: {
	source: dagger.#Artifact

	image: #up: [
		op.#DockerBuild & {context: source},
	]
}

#Run: {
	args: [...string]

	// image may be a remote image ref, or a computed artifact
	{
		image: #Ref
		out: #up: [
			op.#FetchContainer & {ref: image},
			op.#Exec & {"args":        args},
		]

	} | {
		image: _
		out: #up: [
			op.#Load & {from:   image},
			op.#Exec & {"args": args},
		]
	}
}
