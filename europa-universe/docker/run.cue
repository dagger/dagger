package docker

import (
	"list"

	"dagger.io/dagger"
	"dagger.io/dagger/engine"
)

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
