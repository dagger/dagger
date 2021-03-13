package docker

import (
	"dagger.io/dagger"
	"dagger.io/llb"
)

#Ref: string

// Build a docker container image
#Build: {
	source: dagger.#Dir

	image: #compute: [
		llb.#DockerBuild & {context: source},
	]
}

#Run: {
	args: [...string]

	// image may be a remote image ref, or a computed artifact
	{
		image: #Ref
		out: #compute: [
			llb.#FetchContainer & {ref: image},
			llb.#Exec & {"args":        args},
		]

	} | {
		image: _
		out: #compute: [
			llb.#Load & {from:   image},
			llb.#Exec & {"args": args},
		]
	}
}
