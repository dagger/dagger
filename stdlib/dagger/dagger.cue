package dagger

// An artifact such as source code checkout, container image, binary archive...
// May be passed as user input, or computed by a buildkit pipeline
#Artifact: #compute: [...#Op]

// deprecated, use #Artifact instead.
#Dir: #Artifact

// Secret value
// FIXME: currently aliased as a string to mark secrets
// this requires proper support.
#Secret: string

// One operation in a script
#Op: #FetchContainer | #FetchGit | #Export | #Exec | #Local | #Copy | #Load | #Subdir | #WriteFile | #Mkdir

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
	from: _
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
	mount: [string]: "tmpfs" | "cache" | {from: _, path: string | *"/"}
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
