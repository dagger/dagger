package main

import (
	"dagger.io/dagger"

	"universe.dagger.io/bash"
)

dagger.#Plan & {
	// Context: Client API
	client: {
		// Context: Interact with filesystem on host
		filesystem: {
			// Context: key here could be anything. Useful to track step in logs
			"./tmp-example": {
				// 3. write to host filesystem with the content of `export: directories: /tmp` key
				// We access the `/tmp` fs by referencing its key
				write: contents: actions.create.export.directories."/tmp"
			}
		}
	}

	actions: {
		// Context: Action named `create` that creates a file inside a container and exports the `/tmp` dir
		create: bash.#RunSimple & {
			// 1. Create a file in /tmp directory
			script: contents: """
				echo test > /tmp/example
				"""

			// 2. Export `/tmp` dir in container and make it accessible to any other action as a `dagger.#FS`
			export: directories: "/tmp": dagger.#FS

			// Context: Make sure action always gets executed (idempotence)
			always: true
		}
	}
}
