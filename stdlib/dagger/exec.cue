package dagger

// Execute a command in a container
#Exec: {
	// Reserved for runtime use
	_execID: string

	// Base filesystem copied before command execution. Changes are preserved.
	fs: #FS

	// Filesystem overlay mounted before command execution. Changes are discarded.
	mount: [path=string]: #Mountable

       // Command to execute
       command: [...string] | string

       // Environment variables
       environ: [string]: string

       // Working directory
       workdir: string | *"/"

       // Exit code (filled after execution)
       exit: int

       // Optionally attach to command standard input stream
       stdin?: #Stream

       // Optionally attach to command standard output stream
       stdout?: #Stream

       // Optionally attach to command standard error stream
       stderr?: #Stream
}

// Things that can be mounted as a transient filesystem layer
#Mountable: #TempDir | #CacheDir | #Service | #Secret


// A (best effort) persistent cache dir
#CacheDir: {
	// Reserved for runtime use
	_cacheDirID: string

	concurrency: *"shared" | "private" | "locked"
}

// A temporary directory for command execution
#TempDir: {
	// Reserved for runtime use
	_tempDirID: string

	size?: int64
}
