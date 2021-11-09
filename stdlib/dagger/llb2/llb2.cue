package llb2

// A filesystem state
#FS: {
	_fsID: string
} | null

// A stream of bytes
#Stream: {
	_streamID: string
}

// An external secret
#Secret: {
	_secretID: string
}

// An external network service
#Service: {
	_serviceID: string
}

// Import a directory.
// Files are streamed via the builkdkit grpc transport.
#Import: {
	_importID: string

	include?: [...string]
	exclude?: [...string]
}

// Export a directory.
// Files are streamed via the builkdkit grpc transport.
#Export: {
	_exportDirID: string

	// Contents to export
	input: #FS
}

// Execute a command in a container
#Exec: {
	_execID: string

	// Container filesystem
	fs: #FS

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

	// Result of execution
	output: {
		// Command exit code
		exit: int

		// Modified filesystem
		fs: #FS
	}
}

// A transient filesystem mount.
#Mount: {
	_mountID: string

	dest: string
	{
		source: #CacheDir | #TempDir | #Service
	} | {
		source:  #FS
		subdir?: string
		ro:      true | *false
	} | {
		source:   #Secret
		uid:      uint32 | *0
		gid:      uint32 | *0
		optional: true | *false
	}
}

// A (best effort) persistent cache dir
#CacheDir: {
	_cacheDirID: string

	concurrency: *"shared" | "private" | "locked"
}

// A temporary directory for command execution
#TempDir: {
	_tempDirID: string

	size?: int64
}

// Push a directory to a git remote
#GitPush: {
	_gitPushID: string

	input:  #FS
	remote: string
	ref:    string
}

// Pull a directory from a git remote
#GitPull: {
	_gitPullID: string

	remote: string
	ref:    string
	output: #FS
}

// Push a filesystem tree to an OCI repository
#DockerPush: {
	_dockerPushID: string

	// Filesystem contents to push
	input: #FS
	// Target repository address
	target: string
	// Complete ref after push, including digest
	ref: string
	// Authentication
	auth: [...{
		target:   string
		username: string
		secret:   string | #Secret
	}]
}

// Pull a Docker image from a remote repository
#DockerPull: {
	_dockerPullID: string

	// Repository source ref
	source: string
	// Authentication
	auth: [...{
		target:   string
		username: string
		secret:   string | #Secret
	}]
	// The actual ref pulled, including digest
	output: {
		fs:  #FS
		ref: #OCIRef
	}
}

#OCIRef: string

// Build a Docker image
#DockerBuild: {
	_dockerBuildID: string

	// Source directory to build
	source: #FS
	{
		frontend:   "dockerfile"
		dockerfile: {
			path: string | *"Dockerfile"
		} | {
			contents: string
		}
	}

	output: #FS
	// FIXME: expose additional information about the build
	// Ideally there is enough information to 
}

#ReadFile: {
	_readFileID: string

	input:    #FS
	path:     string
	contents: string
	output:   #FS
}

#WriteFile: {
	_writeFileID: string

	input:    #FS
	path:     string
	contents: string
	output:   #FS
}

#Copy: {
	_copyID: string

	input: #FS
	source: {
		fs:   #FS
		path: string | *"/"
	}
	output: #FS
}
