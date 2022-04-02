package core

import "dagger.io/dagger"

// Access the source directory for the current CUE package
// This may safely be called from any package
#Source: {
	$dagger: task: _name: "Source"

	// Relative path to source.
	path: string
	// Optionally exclude certain files
	include: [...string]
	// Optionally include certain files
	exclude: [...string]

	output: dagger.#FS
}

// Create one or multiple directory in a container
#Mkdir: {
	$dagger: task: _name: "Mkdir"

	// Container filesystem
	input: dagger.#FS

	// Path of the directory to create
	// It can be nested (e.g : "/foo" or "/foo/bar")
	path: string

	// Permissions of the directory
	permissions: *0o755 | int

	// If set, it creates parents' directory if they do not exist
	parents: *true | false

	// Modified filesystem
	output: dagger.#FS
}

#ReadFile: {
	$dagger: task: _name: "ReadFile"

	// Filesystem tree holding the file
	input: dagger.#FS
	// Path of the file to read
	path: string
	// Contents of the file
	contents: string
}

// Write a file to a filesystem tree, creating it if needed
#WriteFile: {
	$dagger: task: _name: "WriteFile"

	// Input filesystem tree
	input: dagger.#FS
	// Path of the file to write
	path: string
	// Contents to write
	contents: string
	// Permissions of the file
	permissions: *0o600 | int
	// Output filesystem tree
	output: dagger.#FS
}

// Copy files from one FS tree to another
#Copy: {
	$dagger: task: _name: "Copy"
	// Input of the operation
	input: dagger.#FS
	// Contents to copy
	contents: dagger.#FS
	// Source path (optional)
	source: string | *"/"
	// Destination path (optional)
	dest: string | *"/"
	// Optionally exclude certain files
	include: [...string]
	// Optionally include certain files
	exclude: [...string]
	// Output of the operation
	output: dagger.#FS
}

#CopyInfo: {
	source: {
		root: dagger.#FS
		path: string | *"/"
	}
	dest: string
}

// Merge multiple FS trees into one
#Merge: {
	$dagger: task: _name: "Merge"
	inputs: [...dagger.#FS]
	output: dagger.#FS
}

// Extract the difference from lower FS to upper FS as its own FS
#Diff: {
	$dagger: task: _name: "Diff"
	lower:  dagger.#FS
	upper:  dagger.#FS
	output: dagger.#FS
}

// Select a subdirectory from a filesystem tree
#Subdir: {
	// Input tree
	input: dagger.#FS

	// Path of the subdirectory
	// Example: "/build"
	path: string

	// Copy action
	_copy: #Copy & {
		"input":  dagger.#Scratch
		contents: input
		source:   path
		dest:     "/"
	}

	// Subdirectory tree
	output: dagger.#FS & _copy.output
}
