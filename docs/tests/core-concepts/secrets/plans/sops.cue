dagger.#Plan & {
	client: commands: sops: {
		name: "sops"
		args: ["-d", "./secrets.yaml"]
		stdout: dagger.#Secret
	}

	actions: {
		// Makes the yaml keys easily accessible
		secrets: dagger.#DecodeSecret & {
			input:  client.commands.sops.stdout
			format: "yaml"
		}

		run: docker.#Run & {
			mounts: secret: {
				dest:     "/run/secrets/token"
				contents: secrets.output.myToken
			}
			// Do something with `/run/secrets/token`
		}
	}
}
