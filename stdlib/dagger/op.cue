package dagger

// Each operation is a node in the buildkit DAG
//
// FIXME: #Op does not current enforce the op spec at full resolution, to avoid
// FIXME: triggering performance issues. See https://github.com/dagger/dagger/issues/445
// FIXME: To enforce the full #Op spec (see op_fullop.cue), run with "-t fullop"
#Op: {
	do: string
	...
}

// Export a value from fs state to cue
#ReadFile: {
	do: "readfile"
	input: #Op
	path: string
	contents: string
}

#Mount: {
	do: "mount"
	input: #Op

	// Destination path
	dest: string
	readonly: true | *false

	#BindMount | #SecretMount | #TmpfsMount | #CacheMount | #LocalDirMount
	// Bind mounts are "regular" mounts from one op to another
	#BindMount: {
		type?: "bind"
		// Op from which to mount
		from: #Op
		// Source path
		source: string | *"/"
	}
	#SecretMount: {
		type: "secret"
		from: #Secret

		uid: uint32 | *0
		gid: uint32 | *0
		mode: uint32 | *0x180
	}
	#TmpfsMount: {
		type: "tmpfs"

		// Maximum size in bytes
		size?: int64
	}
	#CacheMount: {
		type: "cache"
		from: #CacheDir
	}
	#LocalDirMount: {
		type: "localdir"
		from: #LocalDir
	}
}

// Proxy an external service into a container
#Proxy: {
	do: "proxy"

	{
		// Proxy service to a unix socket
		type: "unix"
		dest: string
	} | {
		// Proxy service to a tcp port
		type: "tcp"
		port: uint32
	} | {
		// Proxy service to a udp port
		type: "udp"
		port: uint32
	}
}

// Extract a subdirectory from another node
#Subdir: {
	do:  "subdir"
	input: #Op
	dir: string
}

#Exec: {
	do: "exec"
	input: #Op
	args: [...string]
	env?: [string]: string
	// `true` means also ignoring the mount cache volumes
	always?: true | *false
	dir:     string | *"/"
	// Map of hostnames to ip
	hosts?: [string]: string
	// User to exec with (if left empty, will default to the set user in the image)
	user?: string
}

// Pull from OCI container repository
#FetchContainer: {
	do:  "fetch-container"
	ref: string
	auth: #OCIAuth
	metadata: #OCIMetadata
}

// Push input to the target OCI repository
#PushContainer: {
	do:  "push-container"
	input: #Op
	ref: string
	auth: #OCIAuth
	metadata: #OCIMetadata
}


// Save buildkit state as an OCI image tar archive
#SaveImage: {
	do:   "save-image"
	input: #Op
	tag:  string
	dest: string
}

#FetchGit: {
	do:          "fetch-git"
	remote:      string
	ref:         string
	keepGitDir?: bool
	authToken?:  #Secret
	authHeader?: #Secret
}

#FetchHTTP: {
	do:        "fetch-http"
	url:       string
	checksum?: string
	filename?: string
	mode?:     int | *0o644
	uid?:      int
	gid?:      int
}

#Copy: {
	do:   "copy"
	input: #Op
	from: #Op
	src:  string | *"/"
	dest: string | *"/"
}

#DockerBuild: {
	do: "docker-build"
	// We accept either a context, a Dockerfile or both together
	context?:        #Op
	dockerfilePath?: string // path to the Dockerfile (defaults to "Dockerfile")
	dockerfile?:     string

	platforms?: [...string]
	buildArg?: {
		[string]: string | #Secret
	}
	label?: [string]: string
	target?: string
	hosts?: [string]: string
}

#WriteFile: {
	do: "write-file"
	input: #Op
	contents: string | bytes
	dest:    string
	mode:    int | *0o644
}

#Mkdir: {
	do:   "mkdir"
	input: #Op
	dir:  *"/" | string
	path: string
	mode: int | *0o755
}
