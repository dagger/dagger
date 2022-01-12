package main

import (
	"dagger.io/dagger/engine"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

engine.#Plan & {
	// should succeed
	proxy: dockerSocket: unix: "/var/run/docker.sock"

	actions: test: #up: [
		op.#Load & {
			from: alpine.#Image & {
				package: "docker-cli": true
			}
		},

		op.#Exec & {
			always: true
			mount: "/var/run/docker.sock": stream: proxy.dockerSocket.service
			args: ["docker", "info"]
		},
	]
}
