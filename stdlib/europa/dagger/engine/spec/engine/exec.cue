package engine

// Execute a command in a container
#Exec: {
	_exec: {}

	// Container filesystem
	input: #FS

	// Mounts
	mounts: [...#Mount]

	// Command to execute
	args: [...string] | string

	// Environment variables
	environ: [...string]

	// Working directory
	workdir?: string

	// Optionally attach to command standard input stream
	stdin?: #Stream

	// Optionally attach to command standard output stream
	stdout?: #Stream

	// Optionally attach to command standard error stream
	stderr?: #Stream

	// Modified filesystem
	output: #FS

	// Command exit code
	exit: int
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
