package main

import (
	"alpha.dagger.io/dagger/engine"
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/alpine"
)

engine.#Plan & {
	// should fail because of misspelled key
	context: services: dockerSocket: unx: "/var/run/docker.soc"

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
