// op: low-level operations for Dagger processing pipelines
package op

// One operation in a pipeline
//
// FIXME: #Op does not current enforce the op spec at full resolution, to avoid
// FIXME: triggering performance issues. See https://github.com/dagger/dagger/issues/445
// FIXME: To enforce the full #Op spec (see op_fullop.cue), run with "-t fullop"
#Op: {
	do: string
	...
}

// Export a value from fs state to cue
#Export: {
	do: "export"
	// Source path in the container
	source: string
	format: "json" | "yaml" | *"string"
}

#Local: {
	do:  "local"
	dir: string
	include: [...string]
	exclude: [...string]
}

// FIXME: bring back load (more efficient than copy)

#Load: {
	do:   "load"
	from: _
}

#Subdir: {
	do:  "subdir"
	dir: string
}

#Workdir: {
	do:   "workdir"
	path: string
}

#Exec: {
	do: "exec"
	args: [...string]
	env?: [string]: string
	// `true` means also ignoring the mount cache volumes
	always?: true | *false
	dir:     string | *"/"
	// HACK: FIXME later [Performance related]
	// mount: [string]: "tmpfs" | "cache" | {from: _, path: string | *"/"} | {secret: _}
	// https://github.com/dagger/dagger/issues/856
	mount: [string]: {
		_
		...
	}
	// Map of hostnames to ip
	hosts?: [string]: string
	// User to exec with (if left empty, will default to the set user in the image)
	user?: string
}

#DockerLogin: {
	do:       "docker-login"
	target:   string
	username: string
	// FIXME: should be a #Secret (circular import)
	secret: string | bytes
}

#FetchContainer: {
	do:  "fetch-container"
	ref: string
}

#PushContainer: {
	do:  "push-container"
	ref: string
}

#FetchGit: {
	do:          "fetch-git"
	remote:      string
	ref:         string
	keepGitDir?: bool
	// FIXME: the two options are currently ignored until we support buildkit secrets
	authToken?:  _ @dagger(secret)
	authHeader?: _ @dagger(secret)
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
	from: _
	src:  string | *"/"
	dest: string | *"/"
}

#DockerBuild: {
	do: "docker-build"
	// We accept either a context, a Dockerfile or both together
	context?:        _
	dockerfilePath?: string // path to the Dockerfile (defaults to "Dockerfile")
	dockerfile?:     string

	platforms?: [...string]
	buildArg?: [string]: string
	label?: [string]:    string
	target?: string
	hosts?: [string]: string
}

#WriteFile: {
	do: "write-file"
	// FIXME: "contents" to follow english convention
	content: string | bytes
	dest:    string
	mode:    int | *0o644
}

#Mkdir: {
	do:   "mkdir"
	dir:  *"/" | string
	path: string
	mode: int | *0o755
}
