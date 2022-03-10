dagger.#Plan & {
	client: filesystem: "//./pipe/docker_engine": read: {
		contents: dagger.#Service
		type:     "npipe"
	}

	actions: {
		image: alpine.#Build & {
			packages: "docker-cli": {}
		}
		run: docker.#Run & {
			input: image.output
			mounts: docker: {
				dest:     "/var/run/docker.sock"
				contents: client.filesystem."//./pipe/docker_engine".read.contents
			}
			command: {
				name: "docker"
				args: ["info"]
			}
		}
	}
}
