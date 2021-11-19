// Dagger core types
package dagger

import (
	"alpha.dagger.io/dagger/op"
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
#Stream: {
	@dagger(stream)

	id: string
}

// Secret value
#Secret: {
	@dagger(secret)

	id: string
}

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
