package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

// This action builds a docker image from a python app.
// Build steps are defined in native CUE.
#PythonBuild: {
	// Source code of the Python application
	app: dagger.#FS

	// Resulting container image
	image: _build.output

	// Build steps
	_build: docker.#Build & {
		steps: [
			docker.#Pull & {
				source: "python:3.9"
			},
			docker.#Copy & {
				contents: app
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

// Example usage in a plan
dagger.#Plan & {
	client: filesystem: "./src": read: contents: dagger.#FS

	actions: build: #PythonBuild & {
		app: client.filesystem."./src".read.contents
	}
}
