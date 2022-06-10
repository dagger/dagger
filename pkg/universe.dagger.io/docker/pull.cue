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

	// When to pull the image
	resolveMode: *"default" | "forcePull" | "preferLocal"

	// Registry authentication
	auth?: {
		username: string
		secret:   dagger.#Secret
	}

	platform?: string

	_op: core.#Pull & {
		"source":      source
		"resolveMode": resolveMode
		if auth != _|_ {
			"auth": auth
		}
		if platform != _|_ {
			"platform": platform
		}
	}

	// Downloaded image
	image: #Image & {
		rootfs:   _op.output
		config:   _op.config
		platform: _op.platform
	}

	// FIXME: compat with Build API
	output: image
}
