package main

import (
	"dagger.io/dagger"
	// "alpha.dagger.io/dagger/op"
	// "alpha.dagger.io/alpine"
)

dagger.#Plan & {
	// should fail because of misspelled key
	proxy: dockerSocket: unx: "/var/run/docker.sock"

	// TODO/FIXME: Use Europa constructs for this
	//  actions: test: #up: [
	//   op.#Load & {
	//    from: alpine.#Image & {
	//     package: "docker-cli": true
	//    }
	//   },

	//   op.#Exec & {
	//    always: true
	//    mount: "/var/run/docker.sock": stream: proxy.dockerSocket.service
	//    args: ["docker", "info"]
	//   },
	//  ]
}
