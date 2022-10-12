package todoapp

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
	"alpha.dagger.io/http"
)

// docker local socket
dockerSocket: dagger.#Stream & dagger.#Input

// run our todoapp in our local Docker engine
run: docker.#Run & {
	ref:  push.ref
	name: "todoapp"
	ports: ["8080:80"]
	socket: dockerSocket
}

// run our local registry
registry: docker.#Run & {
	ref:  "registry:2"
	name: "registry-local"
	ports: ["5042:5000"]
	socket: dockerSocket
}

// As we pushed the registry to our local docker
// we need to wait for the container to be up
wait: http.#Wait & {
	url: "localhost:5042"
}

// push to our local registry
// this concrete value satisfies the string constraint
// we defined in the previous file
push: target: "\(wait.url)/todoapp"

// Application URL
appURL: "http://localhost:8080/" & dagger.#Output
