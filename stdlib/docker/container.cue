// docker: build and run Docker containers
// https://docker.com

package docker

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

// Run a Docker container
#Container: {

	// Container image
	image: dagger.#Artifact | *#DefaultImage

	// Independently cacheable setup commands
	setup: [...string]

	// Command to execute
	command: string

	// Environment variables shared by all commands
	env: [string]: string

	// Directory in which the command is executed
	dir: string | *"/"

	// Directory to expose as the output.
	// By default the root filesystem is the output.
	outputDir: string | *"/"

	// If true, the command is never cached.
	// (false by default).
	always: true | *false

	// External volumes. There are 4 types:
	//
	// 1. "mount": mount any artifact.
	//     Changes are not included in the final output.
	//
	// 2. "copy": copy any artifact.
	//     Changes are included in the final output.
	//
	// 3. "tmpfs": create a temporary directory.
	//
	// 4. "cache": create a persistent cache diretory.
	//
	volume: [name=string]: {
		// Destination path
		dest: string | *"/"

		*{
			type:   "mount"
			from:   dagger.#Artifact
			source: string | *"/"
		} | {
			type:   "copy"
			from:   dagger.#Artifact
			source: string | *"/"
		} | {
			type: "tmpfs" | "cache"
		}
	}

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

	// Export values from the container to the cue configuration
	export: *null | {
		source: string
		format: op.#Export.format
	}

	#up: [
		op.#Load & {from: image},
		// Copy volumes with type=copy
		for _, v in volume if v.type == "copy" {
			op.#Copy & {
				from: v.from
				dest: v.dest
				src:  v.source
			}
		},
		// Execute setup commands, then main command
		for cmd in setup + [command] {
			op.#Exec & {
				args:     [shell.path] + shell.args + [cmd]
				"env":    env
				"dir":    dir
				"always": always
				mount: {
					for _, v in volume if v.type == "cache" {
						"\(v.dest)": "cache"
					}
					for _, v in volume if v.type == "tmpfs" {
						"\(v.dest)": "tmpfs"
					}
					for _, v in volume if v.type == "mount" {
						"\(v.dest)": {
							from: v.from
							path: v.source
						}
					}
				}
			}
		},
		op.#Subdir & {
			dir: outputDir
		},
		if export != null {
			op.#Export & {
				source: export.source
				format: export.format
			}
		},
	]
}
