dagger.#Plan & {
	// Path may be absolute or relative to the current working directory
	client: filesystem: ".registry.secret": read: {
		// CUE type defines expected content
		contents: dagger.#Secret
	}
	actions: {
		registrySecret: core.#TrimSecret & {
			input: client.filesystem.".registry.secret".read.contents
		}
		pull: docker.#Pull & {
			source: "registry.example.com/image"
			auth: {
				username: "YOUR_USERNAME"
				secret:   registrySecret.output
			}
		}
	}
}
