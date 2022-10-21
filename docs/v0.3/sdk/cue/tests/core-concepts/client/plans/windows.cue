dagger.#Plan & {
	client: network: "npipe:////./pipe/docker_engine": connect: dagger.#Socket

	actions: {
		image: alpine.#Build & {
			packages: "docker-cli": {}
		}
		run: docker.#Run & {
			input: image.output
			mounts: docker: {
				dest:     "/var/run/docker.sock"
				contents: client.network."npipe:////./pipe/docker_engine".connect
			}
			command: {
				name: "docker"
				args: ["info"]
			}
		}
	}
}
