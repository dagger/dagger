package docker

import (
	"dagger.io/dagger"
	"dagger.io/llb"
)

#Ref: string

// Build a docker container image
#Build: {
	source: dagger.#Artifact

	image: #up: [
		llb.#DockerBuild & {context: source},
	]
}

#Run: {
	args: [...string]

	// image may be a remote image ref, or a computed artifact
	{
		image: #Ref
		out: #up: [
			llb.#FetchContainer & {ref: image},
			llb.#Exec & {"args":        args},
		]

	} | {
		image: _
		out: #up: [
			llb.#Load & {from:   image},
			llb.#Exec & {"args": args},
		]
	}
}
