package engine

// Read a file from a filesystem tree
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
	@dagger(notimplemented)
	$dagger: task: _name: "Scratch"

	output: #FS
}

// Create a directory
#Mkdir: {
	@dagger(notimplemented)
	$dagger: task: _name: "Mkdir"

	input: #FS

	// Path of the directory
	path: string
	// FIXME: permissions?
	mode: int
	// Create parent directories as needed?
	parents: *true | false

	output: #FS
}

// Copy files from one FS tree to another
#Copy: {
	// @dagger(notimplemented)
	$dagger: task: _name: "Copy"

	input: #FS
	source: {
		root: #FS
		path: string | *"/"
	}
	dest:   string
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
