package main

import (
	"dagger.io/dagger/op"
	"dagger.io/docker"
)

TestDockerSocket: #up: [
	op.#Load & {
		from: docker.#Client
	},

	op.#Exec & {
		always: true
		mount: "/var/run/docker.sock": "docker.sock"
		args: ["docker", "info"]
	},
]
