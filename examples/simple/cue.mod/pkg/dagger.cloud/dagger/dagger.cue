package dagger

// A dagger component is a configuration value augmented
// by scripts defining how to compute it, present it to a user,
// encrypt it, etc.

#ComputableStruct: {
	#dagger: compute: [...#Op]
	...
}

#ComputableString: {
	string
	#dagger: compute: [...#Op]
}

#Component: {
	// Match structs
	#dagger: #ComponentConfig
	...
} | {
	// Match embedded scalars
	bool | int | float | string | bytes
	#dagger: #ComponentConfig
}

// The contents of a #dagger annotation
#ComponentConfig: {
	// script to compute the value
	compute?: #Script
}

// Any component can be referenced as a directory, since
// every dagger script outputs a filesystem state (aka a directory)
#Dir: #Component

#Script: [...#Op]

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
	do:      "local"
	dir:     string
	include: [...string] | *[]
}

// FIXME: bring back load (more efficient than copy)

#Load: {
	do:   "load"
	from: #Component | #Script
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
	mount: [string]: #MountTmp | #MountCache | #MountComponent | #MountScript
}

#MountTmp:   "tmpfs"
#MountCache: "cache"
#MountComponent: {
	from: #Component
	path: string | *"/"
}
#MountScript: {
	from: #Script
	path: string | *"/"
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
	from: #Script | #Component
	src:  string | *"/"
	dest: string | *"/"
}
