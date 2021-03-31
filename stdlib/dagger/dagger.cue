package dagger

import (
	"dagger.io/llb"
)

// An artifact such as source code checkout, container image, binary archive...
// May be passed as user input, or computed by a buildkit pipeline
#Artifact: {
	#compute: [...llb.#Op]
	_
	...
}

// Secret value
// FIXME: currently aliased as a string to mark secrets
// this requires proper support.
#Secret: string
