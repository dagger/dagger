package dagger

#Directory: #FS

// The internal state of a DAG
#DAG: {
	// Reserved for runtime use
	_dagID: string

	// Base filesystem copied before command execution. Changes are preserved.
	fs: #FS

	// Optionally execute a command
	exec: {
		// Filesystem overlay mounted before command execution. Changes are discarded.
		mount: #Mounts

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

	// Export the filesystem state after execution
	export: *null | {
		// Export to an OCI-compatible container registry
		oci: {
			ref:      string
			tag:      string
			contents: #FS
			metadata: #OCIMetadata
		}
	} | {
		// Export to a git repository
		git: {
			remote:   string
			ref:      string
			contents: #FS
		}
	} | {
		// Export to a contextual directory
		context: #ContextDir
	}
}

// Transient filesystem mounts
#Mounts: [path=string]: #ContextDir | #TempDir | #CacheDir | #Service | #Secret | #FS

// Filesystem state
#FS: {
	#ContextDir | #Pull | string | bytes
	[path=string]: #FS
}

// A stream of bytes
#Stream: {
	// Reserved for runtime use
	_streamID: string
}

// An external secret
#Secret: {
	// Reserved for runtime use
	_secretID: string
}

// An external network service
#Service: {
	// Reserved for runtime use
	_serviceID: string
}

#Build: {
	// Reserved for runtime use
	_dockerBuildID: string
	source: #FS
	// FIXME: support more buildkit frontends
	frontend: "dockerfile"
}

#Fetch: {
	_fetchID: string

	{
		oci: {
			ref: string
		}
	} | {
		git: {
			remote: string
			ref: string
		}
	} | {
		https: {
			url: string
			digest?: string
		}
	}
}

// An external directory
// The contents are streamed via the buildkit grpc API
#ContextDir: {
	// Reserved for runtime use
	_contextDirID: string

	include?: [...string]
	exclude?: [...string]
}

// A (best effort) persistent cache dir
#CacheDir: {
	// Reserved for runtime use
	_cacheDirID: string

	concurrency: *"shared" | "private" | "locked"
}

#TempDir: {
	// Reserved for runtime use
	_tempDir: true

	size?: int64
}
