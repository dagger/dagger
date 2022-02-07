package docker

import (
	"list"

	"dagger.io/dagger"
	"dagger.io/dagger/engine"
)

// Run a command in a container
#Run: {
	_image: #Image

	{
		image:  #Image
		_image: image
	} | {
		// For compatibility with #Build
		input:  #Image
		_image: input
	}

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
	// FIXME: by default, execute the image entrypoint
	command?: {
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

	// Environment variables
	// Example: {"DEBUG": "1"}
	env: [string]: string

	// Working directory for the command
	// Example: "/src"
	workdir: string

	// Username or UID to ad
	// User identity for this command
	// Examples: "root", "0", "1002"
	user: string

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

		export: {
			rootfs: dagger.#FS & _exec.output
			files: [path=string]: {
				contents: string & _read.contents
				_read:    engine.#ReadFile & {
					input:  _exec.output
					"path": path
				}
			}
			directories: [path=string]: {
				contents: dagger.#FS & _subdir.output
				_subdir:  dagger.#Subdir & {
					input:  _exec.output
					"path": path
				}
			}
		}
	}

	// For compatibility with #Build
	output: #Image & {
		rootfs: _exec.output
		config: _image.config
	}

	// Actually execute the command
	_exec: engine.#Exec & {
		input:    _image.rootfs
		"always": always
		"mounts": mounts

		if command != _|_ {
			args: [command.name] + command._flatFlags + command.args
		}
		if command == _|_ {
			args: list.Concat([
				if _image.config.entrypoint != _|_ {
					_image.config.entrypoint
				},
				if _image.config.cmd != _|_ {
					_image.config.cmd
				},
			])
		}
		"env": env
		if _image.config.env != _|_ {
			for key, val in _image.config.env {
				if env[key] == _|_ {
					env: "\(key)": val
				}
			}
		}
		"workdir": workdir
		if workdir == _|_ && _image.config.workdir != _|_ {
			workdir: _image.config.workdir
		}
		"user": user
		if user == _|_ && _image.config.user != _|_ {
			user: _image.config.user
		}
	}
}
