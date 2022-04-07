dagger.#Plan & {
	// Path may be absolute, or relative to current working directory
	client: filesystem: ".registry": read: {
		// CUE type defines expected content
		contents: dagger.#Secret
	}
	actions: {
		registry: core.#TrimSecret & {
			input: client.filesystem.".registry".read.contents
		}
		pull: docker.#Pull & {
			source: "registry.example.com/image"
			auth: {
				username: "_token_"
				secret:   registry.output
			}
		}
	}
}
