package main

import (
	"alpha.dagger.io/dagger/engine"
	"alpha.dagger.io/docker"
)

engine.#Plan & {
	context: services: dockerSocket: unix: "/var/run/docker.sock"
	actions: nginx: docker.#Run & {
		ref:  "nginxdemos/nginx-hello"
		name: "nginx-hello"
		ports: ["8080:8080"]
		socket: context.services.dockerSocket.service
	}
}
