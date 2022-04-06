package test

import (
	"encoding/json"
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/aws"
	"universe.dagger.io/x/evalsocket@gmail.com/opta"
)

dagger.#Plan & {
	client: {
		filesystem: "./opta": read: contents: dagger.#FS
		env: {
            ACCESS_KEY_ID: dagger.#Secret
            SECRET_ACCESS_KEY: dagger.#Secret
            SESSION_TOKEN: dagger.#Secret
		}
	}
	actions: apply: opta.#Action & {
		action:       "apply"
		environment: "production"
		credentials: aws.#Credentials & {
			accessKeyId: client.env.ACCESS_KEY_ID
			secretAccessKey: client.env.SECRET_ACCESS_KEY
			sessionToken: client.env.SESSION_TOKEN
		}
		configFile:     "opta.yaml"
		extraArgs: "", // "--var changelog=sha"
		source:      client.filesystem."./".read.contents
	}
	actions: destroy: opta.#Action & {
		action:       "destroy"
		env: "production"
		credentials: aws.#Credentials & {
			accessKeyId: client.env.ACCESS_KEY_ID
			secretAccessKey: client.env.SECRET_ACCESS_KEY
			sessionToken: client.env.SESSION_TOKEN
		}
		configFile:     "opta.yaml"
		extraArgs: "", // "--var changelog=sha"
		source:      client.filesystem."./".read.contents
	}
	actions: force_unlock: opta.#Action & {
		action:       "force-unlock"
		env: "production"
		credentials: aws.#Credentials & {
			accessKeyId: client.env.ACCESS_KEY_ID
			secretAccessKey: client.env.SECRET_ACCESS_KEY
			sessionToken: client.env.SESSION_TOKEN
		}
		configFile:     "opta.yaml"
		extraArgs: "", // "--var changelog=sha"
		source:      client.filesystem."./".read.contents
	}
}
