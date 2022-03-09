package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: filesystem: ".": read: contents: dagger.#FS

	actions: build: docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "python:3.9"
			},
			docker.#Copy & {
				contents: client.filesystem.".".read.contents
				dest:     "/app"
			},
			docker.#Run & {
				command: {
					name: "pip"
					args: ["install", "-r", "/app/requirements.txt"]
				}
			},
			docker.#Set & {
				config: cmd: ["python", "/app/app.py"]
			},
		]
	}
}
