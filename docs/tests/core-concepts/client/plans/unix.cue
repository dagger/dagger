dagger.#Plan & {
	client: filesystem: "/var/run/docker.sock": read: contents: dagger.#Service

	actions: {
		image: alpine.#Build & {
			packages: "docker-cli": {}
		}
		run: docker.#Run & {
			input: image.output
			mounts: docker: {
				dest:     "/var/run/docker.sock"
				contents: client.filesystem."/var/run/docker.sock".read.contents
			}
			command: {
				name: "docker"
				args: ["info"]
			}
		}
	}
}
