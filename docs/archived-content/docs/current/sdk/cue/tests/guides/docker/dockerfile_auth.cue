build: docker.#Dockerfile & {
	source: client.filesystem.".".read.contents
	auth: {
		"index.docker.io": {
			username: "example"
			secret:   client.env.REGISTRY_DOCKERIO_PASS
		}
		"registry.gitlab.com": {
			username: "example"
			secret:   client.env.REGISTRY_GITLAB_PASS
		}
	}
}
