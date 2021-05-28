// op: low-level operations for Dagger processing pipelines
package op

// One operation in a pipeline
//
// #Op does not enforce the op spec at full resolution, to avoid triggering performance issues.
// See https://github.com/dagger/dagger/issues/445
#Op: {
	do: string
	...
}

// Full resolution schema enforciong the complete op spec
#OpFull: #Export |
	#FetchContainer |
	#PushContainer |
	#FetchGit |
	#Exec |
	#Local |
	#Copy |
	#Load |
	#Subdir |
	#WriteFile |
	#Mkdir |
	#DockerBuild

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

#Exec: {
	do: "exec"
	args: [...string]
	env?: [string]: string
	// `true` means also ignoring the mount cache volumes
	always?: true | *false
	dir:     string | *"/"
	mount: [string]: "tmpfs" | "cache" | {from: _, path: string | *"/"} | {secret: _}
	// Map of hostnames to ip
	hosts?: [string]: string
	// User to exec with (if left empty, will default to the set user in the image)
	user?: string
}

#DockerLogin: {
	do:       "docker-login"
	target:   string | *"https://index.docker.io/v1/"
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
	authTokenSecret?:  string | bytes
	authHeaderSecret?: string | bytes
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
	do:      "write-file"
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
