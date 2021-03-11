package dagger

// A dagger component is a configuration value augmented
// by scripts defining how to compute it, present it to a user,
// encrypt it, etc.
#Component: {
	#compute: [...#Op]
	_
	...
}

// Any component can be referenced as a directory, since
// every dagger script outputs a filesystem state (aka a directory)
#Dir: #Component

// Secret value
// FIXME: currently aliased as a string to mark secrets
// this requires proper support.
#Secret: string

// One operation in a script
#Op: #FetchContainer | #FetchGit | #Export | #Exec | #Local | #Copy | #Load | #Subdir

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
