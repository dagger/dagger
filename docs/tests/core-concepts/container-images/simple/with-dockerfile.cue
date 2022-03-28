package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	client: filesystem: "./src": read: contents: dagger.#FS

	actions: build: docker.#Dockerfile & {
		// This is the context.
		source: client.filesystem."./src".read.contents

		// Default is to look for a Dockerfile in the context,
		// but let's declare it here.
		dockerfile: contents: #"""
			FROM python:3.9
			COPY . /app
			RUN pip install -r /app/requirements.txt
			CMD python /app/app.py

			"""#
	}
}
