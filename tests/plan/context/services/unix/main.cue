package main

import (
	"alpha.dagger.io/dagger/engine"
	"alpha.dagger.io/dagger/op"
  "alpha.dagger.io/alpine"
)

engine.#Plan & {
	context: services: dockerSocket: unix: "/var/run/docker.sock"

	actions: {
		load: op.#Load & {
			from: alpine.#Image & {
				package: "docker-cli": true
			}
		}

		exec: op.#Exec & {
			always: true
			mount: "/var/run/docker.sock": stream: context.services.dockerSocket.service
			args: ["docker", "info"]
		}
	}
}
