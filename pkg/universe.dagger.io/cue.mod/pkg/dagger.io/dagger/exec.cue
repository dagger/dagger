package dagger

// Execute a command in a container
#Exec: {
	$dagger: task: _name: "Exec"

	// Container filesystem
	input: #FS

	// Transient filesystem mounts
	//   Key is an arbitrary name, for example "app source code"
	//   Value is mount configuration
	mounts: [name=string]: #Mount

	// Command to execute
	// Example: ["echo", "hello, world!"]
	args: [...string]

	// Environment variables
	env: [key=string]: string | #Secret

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
	output: #FS

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
		type:     "service"
		contents: #Service
	} | {
		type:     "fs"
		contents: #FS
		source?:  string
		ro?:      true | *false
	} | {
		type:     "secret"
		contents: #Secret
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
