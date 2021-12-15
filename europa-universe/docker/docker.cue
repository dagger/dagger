// Build, ship and run Docker containers in Dagger
package docker

import (
	"list"

	"dagger.io/dagger/engine"
	"dagger.io/dagger"
)

// A container image
#Image: {
	// Root filesystem of the image.
	rootfs: dagger.#FS

	// Image config
	config: engine.#ImageConfig
}

// Run a command in a container
#Run: {
	image: #Image
	input: image // for compatibility with #Build

	always: bool | *false

	// Filesystem mounts
	mounts: [name=string]: engine.#Mount

	// Expose network ports
	// FIXME: investigate feasibility
	ports: [name=string]: {
		frontend: dagger.#Service
		backend: {
			protocol: *"tcp" | "udp"
			address:  string
		}
	}

	// Command to execute
	cmd: {
		// Name of the command to execute
		// Examples: "ls", "/bin/bash"
		name: string

		// Positional arguments to the command
		// Examples: ["/tmp"]
		args: [...string]

		// Command-line flags represented in a civilized form
		// Example: {"-l": true, "-c": "echo hello world"}
		flags: [string]: (string | true)

		_flatFlags: list.FlattenN([
				for k, v in flags {
				if (v & bool) != _|_ {
					[k]
				}
				if (v & string) != _|_ {
					[k, v]
				}
			},
		], 1)
	}

	// Optionally pass a script to interpret
	// Example: "echo hello\necho world"
	script?: string
	if script != _|_ {
		// Default interpreter is /bin/sh -c
		cmd: *{
			name: "/bin/sh"
			flags: "-c": script
		} | {}
	}

	// Environment variables
	// Example: {"DEBUG": "1"}
	env: [string]: string

	// Working directory for the command
	// Example: "/src"
	workdir: string | *"/"

	// Username or UID to ad
	// User identity for this command
	// Examples: "root", "0", "1002"
	user: string | *"root"

	// Output fields
	{
		// Has the command completed?
		completed: bool & (_exec.exit != _|_)

		// Was completion successful?
		success: bool & (_exec.exit == 0)

		// Details on error, if any
		error: {
			// Error code
			code: _exec.exit

			// Error message
			message: string | *null
		}

		output?: {
			// FIXME: hack for #Build compatibility
			#Image

			rootfs?: dagger.#FS & _exec.output
			files: [path=string]: {
				contents: string
				contents: _read.contents

				_read: engine.#ReadFile & {
					input:  _exec.output
					"path": path
				}
			}
			directories: [path=string]: {
				contents: dagger.#FS
				contents: (dagger.#Subdir & {
					input:  _exec.output
					"path": path
				}).output
			}
		}
	}

	// Actually execute the command
	_exec: engine.#Exec & {
		args:      [cmd.name] + cmd._flatFlags + cmd.args
		input:     image.rootfs
		"mounts":  mounts
		"env":     env
		"workdir": workdir
		"user":    user
	}
}

// A ref is an address for a remote container image
// Examples:
//   - "index.docker.io/dagger"
//   - "dagger"
//   - "index.docker.io/dagger:latest"
//   - "index.docker.io/dagger:latest@sha256:a89cb097693dd354de598d279c304a1c73ee550fbfff6d9ee515568e0c749cfe"
#Ref: engine.#Ref

// Download an image from a remote registry
#Pull: {
	// Source ref.
	source: #Ref

	// Registry authentication
	// Key must be registry address, for example "index.docker.io"
	auth: [registry=string]: {
		username: string
		secret:   dagger.#Secret
	}

	_op: engine.#Pull & {
		"source": source
		"auth": [ for target, creds in auth {
			"target": target
			creds
		}]
	}

	// Downloaded image
	image: #Image & {
		rootfs: _op.output
		config: _op.config
	}

	// FIXME: compat with Build API
	output: image
}

// Upload an image to a remote repository
#Push: {
	// Destination ref
	dest: #Ref

	// Complete ref after pushing (including digest)
	result: #Ref & _push.result

	// Registry authentication
	// Key must be registry address
	auth: [registry=string]: {
		username: string
		secret:   dagger.#Secret
	}

	// Image to push
	image: #Image

	_push: engine.#Push & {
		dest:   dest
		input:  image.rootfs
		config: image.config
	}
}
