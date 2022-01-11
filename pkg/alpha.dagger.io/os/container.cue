package os

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/op"

	"alpha.dagger.io/alpine"
)

// Default image for basic use cases
// FIXME: should just be 'alpine.#Image'.
//   referring to '#.up' is a workaround to a dagger engine bug.
//   see https://github.com/dagger/dagger/issues/304
#DefaultImage: alpine.#Image.#up

// Built-in container implementation, using buildkit
#Container: {

	// Container image
	image: dagger.#Artifact | *#DefaultImage
	//     {
	//      // Optionally fetch a remote image
	//      // eg. "index.docker.io/alpine"
	//      from: string
	//      image: #up: [op.#FetchContainer & { "ref": from }]
	//     } | {}

	// Independently cacheable setup commands
	setup: [...string]

	// Command to execute
	command: string | *""

	// Environment variables shared by all commands
	env: [string]: string

	// Directory in which the command is executed
	dir: string | *"/"

	// If true, the command is never cached.
	// (false by default).
	always: true | *false

	// Copy contents from other artifacts
	copy: [string]: from: dagger.#Artifact

	// Mount contents from other artifacts.
	// Mount is active when executing `command`, but not `setup`.
	mount: [string]: {
		from: dagger.#Artifact
		// FIXME: support source path
	}

	// Safely mount secrets (in cleartext) as non-persistent files
	secret: [string]: dagger.#Secret

	// Mount unix socket or windows npipes to the corresponding path
	socket: [string]: dagger.#Stream

	// Write file in the container
	files: [string]: {
		content: string
		mode:    int | *0o644
	}

	// Mount persistent cache directories
	cache: [string]: true

	// Mount temporary directories
	tmpfs: [string]: true

	// Configure the shell which executes all commands.
	shell: {
		// Path of the shell to execute
		path: string | *"/bin/sh"
		// Arguments to pass to the shell prior to the command
		args: [...string] | *["-c"]
	}

	#up: [
		op.#Load & {from: image},
		// Copy volumes with type=copy
		for dest, o in copy {
			op.#Copy & {
				"dest": dest
				from:   o.from
				// FIXME: support source path
			}
		},
		// Execute setup commands, without volumes
		for cmd in setup {
			op.#Exec & {
				args:  [shell.path] + shell.args + [cmd]
				"env": env
				"dir": dir
			}
		},
		for dest, file in files {
			op.#WriteFile & {
				content: file.content
				mode:    file.mode
				"dest":  dest
			}
		},
		// Execute main command with volumes
		if command != "" {
			op.#Exec & {
				args:     [shell.path] + shell.args + [command]
				"env":    env
				"dir":    dir
				"always": always
				"mount": {
					for dest, o in mount {
						"\(dest)": o
						// FIXME: support source path
					}
					for dest, s in secret {
						"\(dest)": secret: s
					}
					for dest, _ in cache {
						"\(dest)": "cache"
					}
					for dest, _ in tmpfs {
						"\(dest)": "tmpfs"
					}
					for dest, s in socket {
						"\(dest)": stream: s
					}
				}
			}
		},
	]
}
