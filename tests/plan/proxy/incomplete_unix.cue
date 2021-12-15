package main

import (
	"alpha.dagger.io/europa/dagger/engine"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

engine.#Plan & {
	// should succeed
	context: services: dockerSocket: unix: string

	actions: test: #up: [
		op.#Load & {
			from: alpine.#Image & {
				package: "docker-cli": true
			}
		},

		op.#Exec & {
			always: true
			mount: "/var/run/docker.sock": stream: context.services.dockerSocket.service
			args: ["docker", "info"]
		},
	]
}
