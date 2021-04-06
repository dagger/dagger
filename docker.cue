package main

import (
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#ImageFromSource: {
	source: dagger.#Artifact

	#up: [
		op.#DockerBuild & {
			context: source
		},
	]
}

#ImageFromRef: {
	ref: string

	#up: [
		op.#FetchContainer & {
			"ref": ref
		},
	]
}

#ImageFromDockerfile: {
	dockerfile: string
	context:    dagger.#Artifact

	#up: [
		op.#DockerBuild & {
			"context":    context
			"dockerfile": dockerfile
		},
	]
}

#Container: {

	image: dagger.#Artifact

	// Optional setup scripts
	setup: [...string]

	// Environment variables shared by all commands
	env: [string]: string

	volume: [name=string]: {
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

	shell: {
		path: string | *"/bin/sh"
		args: [...string] | *["-c"]
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

	command: string

	dir: string | *"/"

	env: [string]: string

	outputDir: string | *"/"
	always:    true | *false

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
	]
}
