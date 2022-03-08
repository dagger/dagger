package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	client: filesystem: ".": read: contents: dagger.#FS

	actions: build: dagger.#Dockerfile & {
		// This is the context.
		source: client.filesystem.".".read.contents

		// Default is to look for a Dockerfile in the context,
		// but let's declare it here.
		contents: #"""
			FROM python:3.9
			COPY . /app
			RUN pip install -r /app/requirements.txt
			CMD python /app/app.py

			"""#
	}
}
