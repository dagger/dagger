// Dagger core types
package dagger

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/dagger/engine"
)

// An artifact such as source code checkout, container image, binary archive...
// May be passed as user input, or computed by a buildkit pipeline
#Artifact: {
	@dagger(artifact)
	#up: [...op.#Op]
	_
	...
}

// Dagger stream. Can be mounted as a UNIX socket.
#Stream: engine.#Service

// A reference to an external secret, for example:
//  - A password
//  - A SSH private key
//  - An API token
// Secrets are never merged in the Cue tree. They can only be used
// by a special filesystem mount designed to minimize leak risk.
#Secret: engine.#Secret

#Input: {
	@dagger(input)
	_
	...
}

#Output: {
	@dagger(output)
	_
	...
}
