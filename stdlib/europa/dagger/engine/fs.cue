package engine

// Create one or multiple directory in a container
#Mkdir: {
	$dagger: task: _name: "Mkdir"

	// Container filesystem
	input: #FS

	// Path of the directory to create
	// It can be nested (e.g : "/foo" or "/foo/bar")
	path: string

	// Permissions to set
	mode: *0o755 | int

	// If set, it creates parents' directory if they do not exist
	parents: *true | false

	// Modified filesystem
	output: #FS
}

#ReadFile: {
	$dagger: task: _name: "ReadFile"

	// Filesystem tree holding the file
	input: #FS
	// Path of the file to read
	path: string
	// Contents of the file
	contents: string
	// Output filesystem tree
	// FIXME: this is a no-op. No output needed.
	output: #FS
}

// Write a file to a filesystem tree, creating it if needed
#WriteFile: {
	$dagger: task: _name: "WriteFile"

	// Input filesystem tree
	input: #FS
	// Path of the file to write
	path: string
	// Contents to write
	contents: string
	// Permissions of the file
	// FIXME: rename to 'permissions' for consistency
	mode: int
	// Output filesystem tree
	output: #FS
}

// Produce an empty directory
#Scratch: {
	$dagger: task: _name: "Scratch"

	output: #FS
}

// Copy files from one FS tree to another
#Copy: {
	$dagger: task: _name: "Copy"

	input: #FS
	#CopyInfo
	output: #FS
}

#CopyInfo: {
	source: {
		root: #FS
		path: string | *"/"
	}
	dest: string
}

// Merge multiple FS trees into one
#Merge: {
	@dagger(notimplemented)
	$dagger: task: _name: "Merge"

	input: #FS
	layers: [...#CopyInfo]
	output: #FS
}
