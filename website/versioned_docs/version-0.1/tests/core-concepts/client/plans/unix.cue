dagger.#Plan & {
	client: network: "unix:///var/run/docker.sock": connect: dagger.#Socket

	actions: {
		image: alpine.#Build & {
			packages: "docker-cli": {}
		}
		run: docker.#Run & {
			input: image.output
			mounts: docker: {
				dest:     "/var/run/docker.sock"
				contents: client.network."unix:///var/run/docker.sock".connect
			}
			command: {
				name: "docker"
				args: ["info"]
			}
		}
	}
}
