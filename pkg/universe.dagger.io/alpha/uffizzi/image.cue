package uffizzi

import (
	"universe.dagger.io/docker"
)

#Image: {
	// The python version to use
	version: *"latest" | string
	docker.#Pull & {
		source: "uffizzi/cli:\(version)"
	}
}
