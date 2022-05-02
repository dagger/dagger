package core

import "dagger.io/dagger"

// Execute a command in a container
#Exec: {
	$dagger: task: _name: "Exec"

	// Container filesystem
	input: dagger.#FS

	// Transient filesystem mounts
	//   Key is an arbitrary name, for example "app source code"
	//   Value is mount configuration
	mounts: [name=string]: #Mount

	// Command to execute
	// Example: ["echo", "hello, world!"]
	args: [...string]

	// Environment variables
	env: [key=string]: string | dagger.#Secret

	// Working directory
	workdir: string | *"/"

	// User ID or name
	user: string | *"root"

	// If set, always execute even if the operation could be cached
	always: true | *false

	// Inject hostname resolution into the container
	// key is hostname, value is IP
	hosts: [hostname=string]: string

	// Modified filesystem
	output: dagger.#FS @dagger(generated)

	// Command exit code
	// Currently this field can only ever be zero.
	// If the command fails, DAG execution is immediately terminated.
	// FIXME: expand API to allow custom handling of failed commands
	exit: int & 0
}

// Start a command in a container asynchronously
#Start: {
	$dagger: task: _name: "Start"

	// Container filesystem
	input: dagger.#FS

	// Transient filesystem mounts
	//   Key is an arbitrary name, for example "app source code"
	//   Value is mount configuration
	mounts: [name=string]: #Mount

	// Command to execute
	// Example: ["echo", "hello, world!"]
	args: [...string]

	// Working directory
	workdir: string | *"/"

	// User ID or name
	user: string | *"root"

	// Inject hostname resolution into the container
	// key is hostname, value is IP
	hosts: [hostname=string]: string

	// Environment variables
	env: [key=string]: string

	_id: string | null @dagger(generated)
}

// Stop an asynchronous command created by #Start
#Stop: {
	$dagger: task: _name: "Stop"

	input: #Start

	// Command exit code. If the command was still running when
	// stopped, it will be sent SIGKILL and the exit code will
	// thus be 137.
	exit: uint8 @dagger(generated)
}

// A transient filesystem mount.
#Mount: {
	dest: string
	type: string
	{
		type:     "cache"
		contents: #CacheDir
	} | {
		type:     "tmp"
		contents: #TempDir
	} | {
		type:     "socket"
		contents: dagger.#Socket
	} | {
		type:     "fs"
		contents: dagger.#FS
		source?:  string
		ro?:      true | *false
	} | {
		type:     "secret"
		contents: dagger.#Secret
		uid:      int | *0
		gid:      int | *0
		mask:     int | *0o400
	}
}

// A (best effort) persistent cache dir
#CacheDir: {
	id:          string
	concurrency: *"shared" | "private" | "locked"
}

// A temporary directory for command execution
#TempDir: {
	size: int64 | *0
}
