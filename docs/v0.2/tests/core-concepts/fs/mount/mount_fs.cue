package main

import (
	"dagger.io/dagger/sdk/go/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	// client: filesystem: 
	actions: {
		// Create base image for the two actions relying on 
		// docker.#Run universe package (exec and verify)
		_img: alpine.#Build

		// Create a file inside alpine's image
		exec: docker.#Run & {
			// Retrieve the image from the `image` (alpine.#Image) action
			// Make it the base image for this action
			input: _img.output
			// execute a command in this container
			command: {
				name: "sh"
				// create an output.txt file
				flags: "-c": #"""
					echo -n hello world >> /output.txt
					"""#
			}
		}

		// Verify the content of an action
		verify: docker.#Run & {
			// Retrieve the image from the `image` (alpine.#Image) action
			// Make it the base image for this action
			input: _img.output
			// mount the rootfs of the `exec` action after execution
			// (after the /output.txt file has been created)
			// Mount it at /target, in container
			mounts: "example target": {
				dest:     "/target"
				contents: exec.output.rootfs
			}
			command: {
				name: "sh"
				// verify that the /target/output.txt file exists
				flags: "-c": """
					test "$(cat /target/output.txt)" = "hello world"
					"""
			}
		}
	}
}
