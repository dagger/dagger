package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/engine"
)

// Upload an image to a remote repository
#Push: {
	// Destination ref
	dest: #Ref

	// Complete ref after pushing (including digest)
	result: #Ref & _push.result

	// Registry authentication
	// Key must be registry address
	auth: [registry=string]: {
		username: string
		secret:   dagger.#Secret
	}

	// Image to push
	image: #Image

	_push: engine.#Push & {
		"dest": dest
		"auth": [ for target, creds in auth {
			"target": target
			creds
		}]
		input:  image.rootfs
		config: image.config
	}
}
