package main

import (
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

	// Optional setup script
	setup: string | *null

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

	command: [name=string]: {
		args: [...string]
		dir:   string | *"/"
		"env": env & {
			[string]: string
		}
		outputDir: string | *"/"
		always:    true | *false

		// Execute each command in a pristine filesystem state
		// (commands do not interfere with each other's changes)
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
			// Execute setup script
			if setup != null {
				op.#Exec & {
					"env": env
					args: ["/bin/sh", "-c", setup]
				}
			},
			op.#Exec & {
				"args":   args
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
			},
			op.#Subdir & {
				dir: outputDir
			},
		]
	}

}
