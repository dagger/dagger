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
		filesystem: "./": read: contents: dagger.#FS
		env: {
			PULUMI_ACCESS_TOKEN: dagger.#Secret
		}
		commands: sops: {
			name: "sops"
			args: ["-d", "--extract", "[\"AWS\"]", "../../secrets_sops.yaml"]
			stdout: dagger.#Secret
		}
	}
	actions: build: opta.#Build
	actions: soapSecret: core.#DecodeSecret & {
			format: "yaml"
			input:  client.commands.sops.stdout
	}
	actions: apply: opta.#Action & {
		action:       "apply"
		env: "production"
		credentials: aws.#Credentials & {
			accessKeyId:     sopsSecrets.output.AWS_ACCESS_KEY_ID.contents
			secretAccessKey: sopsSecrets.output.AWS_SECRET_ACCESS_KEY.contents
		}
		configFile:     "opta.yaml"
		extraArgs: "", // "--var changelog=sha"
		source:      client.filesystem."./".read.contents
	}
	actions: destroy: opta.#Action & {
		action:       "destroy"
		env: "production"
		credentials: aws.#Credentials & {
			accessKeyId:     sopsSecrets.output.AWS_ACCESS_KEY_ID.contents
			secretAccessKey: sopsSecrets.output.AWS_SECRET_ACCESS_KEY.contents
		}
		configFile:     "opta.yaml"
		extraArgs: "", // "--var changelog=sha"
		source:      client.filesystem."./".read.contents
	}
	actions: force_unlock: opta.#Action & {
		action:       "force-unlock"
		env: "production"
		credentials: aws.#Credentials & {
			accessKeyId:     sopsSecrets.output.AWS_ACCESS_KEY_ID.contents
			secretAccessKey: sopsSecrets.output.AWS_SECRET_ACCESS_KEY.contents
		}
		configFile:     "opta.yaml"
		extraArgs: "", // "--var changelog=sha"
		source:      client.filesystem."./".read.contents
	}
}
