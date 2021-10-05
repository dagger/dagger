package todoapp

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/docker"
)

// run our todoapp in our local Docker engine
run: docker.#Run & {
	ref:  push.ref
	name: "todoapp"
	ports: ["8080:80"]
	socket: dagger.#Stream & dagger.#Input
}

// push to our local registry
// this concrete value satisfies the string constraint
// we defined in the previous file
push: target: "localhost:5000/todoapp"
