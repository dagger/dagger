// Build, ship and run Docker containers in Dagger
package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// Download an image from a remote registry
#Pull: {
	// Source ref.
	source: #Ref

	// Registry authentication
	auth?: {
		username: string
		secret:   dagger.#Secret
	}

	_op: core.#Pull & {
		"source": source
		if auth != _|_ {
			"auth": auth
		}
	}

	// Downloaded image
	image: #Image & {
		rootfs: _op.output
		config: _op.config
	}

	// FIXME: compat with Build API
	output: image
}
