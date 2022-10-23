package main

import (
	"dagger.io/dagger"

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

		// copy the rootfs of the `exec` action after execution
		// (after the /output.txt file has been created)
		// Copy it at /target, in container's rootfs
		_copy: docker.#Copy & {
			input:    _img.output
			contents: exec.output.rootfs
			dest:     "/target"
		}

		// Verify the content of an action
		verify: docker.#Run & {
			// Retrieve the image from the `_copy` action
			// Make it the base image for this action
			input: _copy.output
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
