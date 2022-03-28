package docker

import (
	"dagger.io/dagger"
)

// Change image config
#Set: {
	// The source image
	input: #Image

	// The image config to change
	config: dagger.#ImageConfig

	_set: dagger.#Set & {
		"input":  input.config
		"config": config
	}

	// Resulting image with the config changes
	output: #Image & {
		rootfs: input.rootfs
		config: _set.output
	}
}
