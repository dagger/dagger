package engine

// Read a file from a filesystem tree
#ReadFile: {
	_type: "ReadFile"

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
	_type: "WriteFile"

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
