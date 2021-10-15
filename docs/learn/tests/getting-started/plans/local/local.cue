package todoapp

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
)

// docker local socket
dockerSocket: dagger.#Stream & dagger.#Input

// run our todoapp in our local Docker engine
load: docker.#Load & {
	source: image
	tag:    "todoapp"
	socket: dockerSocket
}

// Application URL
appURL: "http://localhost:8080/" & dagger.#Output
