package engine

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
	env: [key=string]: string

	// Working directory
	workdir: string | *"/"

	// User ID or name
	user: string | *"root"

	// If set, always execute even if the operation could be cached
	always: true | *false

	// Modified filesystem
	output: #FS

	// Command exit code
	// Currently this field can only ever be zero.
	// If the command fails, DAG execution is immediately terminated.
	// FIXME: expand API to allow custom handling of failed commands
	exit: int & 0

	// Inject hostname resolution into the container
	// key is hostname, value is IP
	hosts: [hostname=string]: string
}

// A transient filesystem mount.
#Mount: {
	dest: string
	{
		contents: #CacheDir | #TempDir | #Service
	} | {
		contents: #FS
		source:   string | *"/"
		ro:       true | *false
	} | {
		contents: #Secret
		uid:      uint32 | *0
		gid:      uint32 | *0
		optional: true | *false
	}
}

// A (best effort) persistent cache dir
#CacheDir: {
	id:          string
	concurrency: *"shared" | "private" | "locked"
}

// A temporary directory for command execution
#TempDir: {
	size?: int64
}
