package docker

import (
	"list"

	"dagger.io/dagger"
)

// Run a command in a container
#Run: {
	// Docker image to execute
	input: #Image

	always: bool | *false

	// Filesystem mounts
	mounts: [name=string]: dagger.#Mount

	// Expose network ports
	// FIXME: investigate feasibility
	ports: [name=string]: {
		frontend: dagger.#Service
		backend: {
			protocol: *"tcp" | "udp"
			address:  string
		}
	}

	// Entrypoint to prepend to command
	entrypoint?: [...string]

	// Command to execute
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
	env: [string]: string | dagger.#Secret

	// Working directory for the command
	// Example: "/src"
	workdir: string

	// Username or UID to ad
	// User identity for this command
	// Examples: "root", "0", "1002"
	user: string

	// Add defaults to image config
	// This ensures these values are present
	_defaults: dagger.#Set & {
		"input": {
			entrypoint: []
			cmd: []
			workdir: "/"
			user:    "root"
		}
		config: input.config
	}

	// Override with user config
	_config: dagger.#Set & {
		input: _defaults.output
		config: {
			if entrypoint != _|_ {
				"entrypoint": entrypoint
			}
			if command != _|_ {
				cmd: [command.name] + command._flatFlags + command.args
			}
			if workdir != _|_ {
				"workdir": workdir
			}
			if user != _|_ {
				"user": user
			}
		}
	}

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
			files: [path=string]: string
			_files: {
				for path, _ in files {
					"\(path)": {
						contents: string & _read.contents
						_read:    dagger.#ReadFile & {
							input:  _exec.output
							"path": path
						}
					}
				}
			}
			for path, output in _files {
				files: "\(path)": output.contents
			}

			directories: [path=string]: dagger.#FS
			_directories: {
				for path, _ in directories {
					"\(path)": {
						contents: dagger.#FS & _subdir.output
						_subdir:  dagger.#Subdir & {
							input:  _exec.output
							"path": path
						}
					}
				}
			}
			for path, output in _directories {
				directories: "\(path)": output.contents
			}
		}
	}

	// For compatibility with #Build
	output: #Image & {
		rootfs: _exec.output
		config: input.config
	}

	// Actually execute the command
	_exec: dagger.#Exec & {
		"input":  input.rootfs
		"always": always
		"mounts": mounts
		args:     _config.output.entrypoint + _config.output.cmd
		workdir:  _config.output.workdir
		user:     _config.output.user
		"env":    env
		// env may contain secrets so we can't use dagger.#Set
		if input.config.env != _|_ {
			for key, val in input.config.env {
				if env[key] == _|_ {
					env: "\(key)": val
				}
			}
		}
	}

	// Command exit code
	exit: _exec.exit
}
