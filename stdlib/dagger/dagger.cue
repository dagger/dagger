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

// Dagger socket. Can be mounted as a UNIX socket.
#Socket: {
	@dagger(socket)

	unix: string
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
