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

	command: #Command
	command: {
		exit: _exec.exit
		stdout: _exec.stdout
		stderr: _exec.stderr

		// Optionally create a new container from the modified filesystem
		output?: #Container & {
			fs: _exec
		}

		_exec: llb2.#Exec & {
			"fs": fs
			"mounts": [for mnt in mount { mnt }]
			"args": command.args
			"environ": [for k, v in command.env { "\(k)=\(v)" }]
			"workdir": command.workdir
			"stdin": command.stdin
		}
	}
}

#Command: {
	container: #Container

	args: [...string] | string
	env: [string]: string
	workdir: string | *"/"
	user: string

	// Optionally attach to command standard streams
	stdin: llb2.#Stream | *null
	stdout: llb2.#Stream | *null
	stderr: llb2.#Stream | *null

	// Exit code (filled after execution)
	exit: int

	_exec: llb2.#Exec & {
		fs: container.fs
		mounts: [for mnt in container.mount { mnt }]
			"args": command.args
			"environ": [for k, v in command.env { "\(k)=\(v)" }]
			"workdir": command.workdir
			"stdin": command.stdin

	}
}


#File: {
	container: #Container
	path: string

	{
		read: string & _llb.contents
		_llb: llb2.#ReadFile & {
			"input": container.fs
			"path": path
		}
	} | {
		write: string
		_llb: llb2.#WriteFile & {
			// FIXME
		}
	}
}
