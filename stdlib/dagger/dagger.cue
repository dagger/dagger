// Dagger core types
package dagger

import (
	"alpha.dagger.io/dagger/op"
)

// A reference to a filesystem tree.
// For example:
//  - The root filesystem of a container
//  - A source code repository
//  - A directory containing binary artifacts
// Rule of thumb: if it fits in a tar archive, it fits in a #FS.
#FS: {
	_fs: id: string
}

// An artifact such as source code checkout, container image, binary archive...
// May be passed as user input, or computed by a buildkit pipeline
#Artifact: {
	@dagger(artifact)
	#up: [...op.#Op]
	_
	...
}

// A reference to a network service endpoint, for example:
//  - A TCP or UDP port
//  - A unix or npipe socket
//  - An HTTPS endpoint
#Service: {
	_service: id: string
}

// Dagger stream. Can be mounted as a UNIX socket.
// FIXME: Deprecated. For backward compatibility only, use #Service instead.
#Stream: #Service

// A reference to an external secret, for example:
//  - A password
//  - A SSH private key
//  - An API token
// Secrets are never merged in the Cue tree. They can only be used
// by a special filesystem mount designed to minimize leak risk.
#Secret: {
	_secret: id: string
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
