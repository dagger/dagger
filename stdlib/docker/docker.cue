package docker

import (
	"dagger.io/dagger"
)

#Ref: string

// Build a docker container image
#Build: {
	source: dagger.#Dir

	image: #compute: [
		dagger.#DockerBuild & {context: source},
	]
}

#Run: {
	args: [...string]

	// image may be a remote image ref, or a computed artifact
	{
		image: #Ref
		out: #compute: [
			dagger.#FetchContainer & {ref: image},
			dagger.#Exec & {"args":        args},
		]

	} | {
		image: _
		out: #compute: [
			dagger.#Load & {from:   image},
			dagger.#Exec & {"args": args},
		]
	}
}
