package docker

import (
	"dagger.io/dagger/engine"
)

// Change image config
#Set: {
	// The source image
	input: #Image

	// The image config to change
	config: engine.#ImageConfig

	_set: engine.#Set & {
		"input":  input.config
		"config": config
	}

	// Resulting image with the config changes
	output: #Image & {
		rootfs: input.rootfs
		config: _set.output
	}
}
