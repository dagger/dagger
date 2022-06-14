scripts: dagger.#FS

_run: docker.#Run & {
	mounts: {
		// description can be anything, just needs to be
		// different from other mounts
		"description here": {
			// destination directory in this image to mount files
			dest: "/opt/scripts"

			// `type: "fs"` not needed because `contents: dagger.#FS`
			// already resolves to the correct type.
			contents: scripts
		}
		node_modules: {
			dest: "/src/node_modules"

			// mounts in different `docker.#Run` with the same
			// cache id should point to the same files
			contents: core.#CacheDir & {
				id: "my-node-modules"
			}
		}
		temp: {
			dest:     "/temp"
			contents: core.#TempDir
		}
	}
	...
}
