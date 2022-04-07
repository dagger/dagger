dagger.#Plan & {
	// Path may be absolute, or relative to current working directory
	client: filesystem: ".": read: {
		// CUE type defines expected content
		contents: dagger.#FS
		exclude: ["node_modules"]
	}

	actions: {
		copy: docker.#Copy & {
			contents: client.filesystem.".".read.contents
		}
		// ...
	}
}
