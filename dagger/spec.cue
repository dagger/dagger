package dagger

// A DAG is the basic unit of programming in dagger.
// It is a special kind of program which runs as a pipeline of computing nodes running in parallel,
// instead of a sequence of operations to be run by a single node.
//
// It is a powerful way to automate various parts of an application delivery workflow:
// build, test, deploy, generate configuration, enforce policies, publish artifacts, etc.
//
// The DAG architecture has many benefits:
//  - Because DAGs are made of nodes executing in parallel, they are easy to scale.
//  - Because all inputs and outputs are snapshotted and content-addressed, DAGs
//  can easily be made repeatable, can be cached aggressively, and can be replayed
//  at will.
//  - Because nodes are executed by the same container engine as docker-build, DAGs
//  can be developed using any language or technology capable of running in a docker.
//  Dockerfiles and docker images are natively supported for maximum compatibility.
//
//  - Because DAGs are programmed declaratively with a powerful configuration language,
//  they are much easier to test, debug and refactor than traditional programming languages.
//
// To execute a DAG, the dagger runtime JIT-compiles it to a low-level format called
// llb, and executes it with buildkit.
// Think of buildkit as a specialized VM for running compute graphs; and dagger as
// a complete programming environment for that VM.
//
// The tradeoff for all those wonderful features is that a DAG architecture cannot be used
// for all software: only software than can be run as a pipeline.
//

// A dagger component is a configuration value augmented
// by scripts defining how to compute it, present it to a user,
// encrypt it, etc.

#Component: {
	// Match structs
	#dagger: #ComponentConfig
	...
} | {
	// Match embedded strings
	// FIXME: match all embedded scalar types
	string
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
#Op: #FetchContainer | #FetchGit | #Export | #Exec | #Local | #Copy | #Load

// Export a value from fs state to cue
#Export: {
	do: "export"
	// Source path in the container
	source: string
	format: "json" | "yaml" | *"string" | "number" | "boolean"
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
