package docker

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

// Upload an image to a remote repository
#Push: {
	// Destination ref
	dest: #Ref

	// Complete ref after pushing (including digest)
	result: #Ref & _push.result

	// Registry authentication
	auth?: {
		username: string
		secret:   dagger.#Secret
	}

	_push: core.#Push & {
		"dest": dest
		if auth != _|_ {
			"auth": auth
		}
	}

	{
		// Image to push
		image: #Image

		_push: {
			input:    image.rootfs
			config:   image.config
			platform: image.platform
		}
	} | {
		// Images to push
		images: [K=string]: #Image

		_push: inputs: {
			for _p, _image in images {
				"\(_p)": {
					input:  _image.rootfs
					config: _image.config
				}
			}
		}
	}
}
