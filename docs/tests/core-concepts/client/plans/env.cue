dagger.#Plan & {
	client: env: {
		REGISTRY_USER:  string
		REGISTRY_TOKEN: dagger.#Secret
	}

	actions: pull: docker.#Pull & {
		source: "registry.example.com/image"
		auth: {
			username: client.env.REGISTRY_USER
			secret:   client.env.REGISTRY_TOKEN
		}
	}
}
