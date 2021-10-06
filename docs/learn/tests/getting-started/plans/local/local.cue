package todoapp

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
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
	ports: ["5000:5000"]
	socket: dockerSocket
}

// push to our local registry
// this concrete value satisfies the string constraint
// we defined in the previous file
push: target: "localhost:5000/todoapp"

// output the application URL
appURL: "http://localhost:8080/" & dagger.#Output
