package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/llb2"
)


dagger.#Plan

context: {
	// My source code
	import: source: _

	// Docker engine endpoint
	services: docker: _

	// Netlify token
	secrets: netlify: _
}

actions: {
	pipeline: dagger.#Command & {
		args: ["wc", "-l"]
		stdin: {
			dagger.#Command & {
				args: "find ."
			}
		}.stdout
	}

	build: #YarnBuild & {
		source: context.import.source.fs
	}
	deploy: #NetlifyDeploy & {
		contents: build.build
		auth: token: context.secrets.netlify.file
	}

	baseimage: llb2.#Exec & {
		fs: llb2.#DockerPull & {
			source: "alpine"
		}
		always: true
		args: "echo $RANDOM > /toto"
	}

	sharedCache: llb2.#CacheDir & {
		concurrency: "locked"
	}

	foo: llb2.#Exec & {
		fs: baseimage
		args: "echo foo"
		mounts: [
			{
				dest: "/data"
				source: sharedCache
			}
		]
	}
	bar: llb2.#Exec & {
		fs: baseimage
		args: "echo bar"
		mounts: [
			{
				dest: "/data"
				source: sharedCache
			}
		]
	}
	baz: llb2.#Exec & {
		fs: baseimage
		args: "echo baz"
		mounts: [
			{
				dest: "/data"
				source: sharedCache
			}
		]
	}

}


////////////
////////////


#YarnBuild: {
	// Input
	source: llb2.#FS
	// Output
	build: llb2.#FS

	// Build container
	_ctr: dagger.#Container & {
		mount: "/app": "source": source
		// FIXME: build image
	}
	_cmd: dagger.#Command & {
		container: _ctr
		args: ["yarn", "help"]
	}


	build: {
		#Subdir & {
			input: _cmd.output.fs
			path: "/build"
		}
	}.out

	build: llb2.#Copy & {
		input: null
		source: {
			fs: _cmd.output.fs
			path: "/build"
		}
	}
}


#Subdir: {
	input: llb2.#FS
	path: string

	llb2.#Copy & {
		"input": null
		source: {
			fs: input
			"path": path
		}
	}
}


#NetlifyDeploy: {
	contents: llb2.#FS
}
