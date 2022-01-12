package dagger

import (
	"dagger.io/dagger/engine"
)

// A reference to a filesystem tree.
// For example:
//  - The root filesystem of a container
//  - A source code repository
//  - A directory containing binary artifacts
// Rule of thumb: if it fits in a tar archive, it fits in a #FS.
#FS: engine.#FS

// A reference to an external secret, for example:
//  - A password
//  - A SSH private key
//  - An API token
// Secrets are never merged in the Cue tree. They can only be used
// by a special filesystem mount designed to minimize leak risk.
#Secret: engine.#Secret

// A reference to a network service endpoint, for example:
//  - A TCP or UDP port
//  - A unix socket
//  - An HTTPS endpoint
#Service: engine.#Service

// A network service address
#Address: engine.#Address
