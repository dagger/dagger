package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/x/benjamin.reigner@epitech.eu/gcp"
	"universe.dagger.io/x/benjamin.reigner@epitech.eu/gcp/gcr"
	"universe.dagger.io/x/benjamin.reigner@epitech.eu/gcp/function"
)

dagger.#Plan & {
	client: {
		env: GCP_PROJECT: string
		filesystem: {
			"./src": read: contents:                     dagger.#FS
			"./secrets/serviceKey.json": read: contents: dagger.#Secret
		}
	}

	actions: {
		_credentials: gcr.#Credentials & {
			config: gcp.#Config & {
				serviceKey: client.filesystem."./secrets/serviceKey.json".read.contents
				project:    client.env.GCP_PROJECT
				region:     "europe-west3"
				zone:       "europe-west3-b"
			}
		}

		HelloWorld: function.#Function & {
			config:  _credentials
			name:    "HelloWorld"
			runtime: "go116"
			source:  client.filesystem."./src".read.contents
		}
	}
}
