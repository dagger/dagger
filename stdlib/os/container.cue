package os

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/op"

	"dagger.io/alpine"
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
		// Map of directories to search for commands
		// In POSIX shells this is used to generate the $PATH
		// environment variable.
		search: [string]: bool
		search: {
			"/sbin":           true
			"/bin":            true
			"/usr/sbin":       true
			"/usr/bin":        true
			"/usr/local/sbin": true
			"/usr/local/bin":  true
		}
	}
	env: PATH: string | *strings.Join([ for p, v in shell.search if v {p}], ":")

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
				args:     [shell.path] + shell.args + [cmd]
				"env":    env
				"dir":    dir
				"always": always
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
						"\(dest)": from: o.from
						// FIXME: support source path
					}
					for dest in cache {
						"\(dest)": "cache"
					}
					for dest in tmpfs {
						"\(dest)": "tmpfs"
					}
				}
			}
		},
	]
}
