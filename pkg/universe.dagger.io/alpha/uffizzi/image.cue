package uffizzi

import (
	"universe.dagger.io/docker"
)

#Image: {
	// The python version to use
	version: *"0.11.2" | string
	docker.#Pull & {
		source: "uffizzi/cli:v\(version)"
	}
}
