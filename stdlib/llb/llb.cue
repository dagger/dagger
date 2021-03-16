// llb: compile LLB graphs executable by buildkit
package llb

// A dagger component is a configuration value augmented
// by scripts defining how to compute it, present it to a user,
// encrypt it, etc.
#Component: {
	#compute: [...#Op]
	_
	...
}

// One operation in a script
#Op: #Export |
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
}

// FIXME: bring back load (more efficient than copy)

#Load: {
	do:   "load"
	from: #Component
}

#Subdir: {
	do:  "subdir"
	dir: string | *"/"
}

#Exec: {
	do: "exec"
	args: [...string]
	env?: [string]: string
	always?: true | *false
	dir:     string | *"/"
	mount: [string]: "tmpfs" | "cache" | {from: #Component, path: string | *"/"}
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
	do:     "fetch-git"
	remote: string
	ref:    string
}

#Copy: {
	do:   "copy"
	from: #Component
	src:  string | *"/"
	dest: string | *"/"
}

#DockerBuild: {
	do: "docker-build"
	// We accept either a context, a Dockerfile or both together
	context?:        #Component
	dockerfilePath?: string // path to the Dockerfile (defaults to "Dockerfile")
	dockerfile?:     string

	platforms?: [...string]
	buildArg?: [string]: string
	label?: [string]:    string
}

#WriteFile: {
	do:      "write-file"
	content: string
	dest:    string
	mode:    int | *0o644
}

#Mkdir: {
	do:   "mkdir"
	dir:  *"/" | string
	path: string
	mode: int | *0o755
}
