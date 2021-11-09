package dagger

import (
	"alpha.dagger.io/dagger/llb2"
)

#Container: {
	fs: llb2.#FS
	mount: [dest=string]: llb2.#Mount & {
		"dest": dest
	}
}

#Command: {
	container: #Container

	args: [...string] | string
	env: [string]: string
	workdir: string | *"/"
	user:    string

	// Optionally attach to command standard streams
	stdin:  llb2.#Stream | *null
	stdout: llb2.#Stream | *null
	stderr: llb2.#Stream | *null

	// Exit code (filled after execution)
	exit: int

	// Optionally produce a new container from modified filesystem
	output?: #Container & {
		fs: _exec.output.fs
	}

	_exec: llb2.#Exec & {
		fs: container.fs
		mounts: [ for mnt in container.mount {mnt}]
		"args": args
		"environ": [ for k, v in env {"\(k)=\(v)"}]
		"workdir": workdir
		"stdin":   stdin
	}
}

#File: {
	container: #Container
	path:      string

	{
		read: string & _llb.contents
		_llb: llb2.#ReadFile & {
			input:  container.fs
			"path": path
		}
	} | {
		write: string
		_llb:  llb2.#WriteFile & {
			input:    container.fs
			"path":   path
			contents: write
		}
	}
}
