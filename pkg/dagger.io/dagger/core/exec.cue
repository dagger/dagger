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
	mounts: {
		[name=string]: #Mount
	} @cloak(notimplemented)

	// Command to execute
	// Example: ["echo", "hello, world!"]
	args: [...string]

	// Environment variables
	env: [key=string]: string | dagger.#Secret

	// Working directory
	workdir: string | *"/"

	// User ID or name
	user: string | *"root:root"

	// If set, always execute even if the operation could be cached
	always: true | *false @cloak(notimplemented)

	// Inject hostname resolution into the container
	// key is hostname, value is IP
	hosts: {
		[hostname=string]: string
	} @cloak(notimplemented)

	// Modified filesystem
	output: dagger.#FS @dagger(generated)

	// Command exit code
	// Currently this field can only ever be zero.
	// If the command fails, DAG execution is immediately terminated.
	// FIXME: expand API to allow custom handling of failed commands
	exit: int & 0
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
		type:     "socket" @cloak(notimplemented)
		contents: dagger.#Socket
	} | {
		type:     "fs"
		contents: dagger.#FS
		source?:  string
		ro?:      true | *false @cloak(notimplemented)
	} | {
		type:     "secret"
		contents: dagger.#Secret
		uid:      int | *0     @cloak(notimplemented)
		gid:      int | *0     @cloak(notimplemented)
		mask:     int | *0o400 @cloak(notimplemented)
	} | {
		type:        "file"
		contents:    string
		permissions: *0o644 | int @cloak(notimplemented)
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
