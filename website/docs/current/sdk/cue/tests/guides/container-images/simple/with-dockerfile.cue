package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

// This action builds a docker image from a python app.
// Build steps are defined in an inline Dockerfile.
#PythonBuild: docker.#Dockerfile & {
	dockerfile: contents: """
		FROM python:3.9
		COPY . /app
		RUN pip install -r /app/requirements.txt
		CMD python /app/app.py
		"""
}

// Example usage in a plan
dagger.#Plan & {
	client: filesystem: "./src": read: contents: dagger.#FS

	actions: build: #PythonBuild & {
		source: client.filesystem."./src".read.contents
	}
}
