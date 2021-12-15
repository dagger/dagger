package engine

// A filesystem state
#FS: {
	_fs: ID: string
}

// Produce an empty directory
// FIXME: replace with a null value for #FS?
#Scratch: {
	_scratch: {}

	output: #FS
}

#ReadFile: {
	_readFile: {}

	input:    #FS
	path:     string
	contents: string
	output:   #FS
}

#WriteFile: {
	_writeFile: {}

	input:    #FS
	path:     string
	contents: string
	output:   #FS
}

// Create a directory
#Mkdir: {
	_mkdir: {}

	input: #FS

	// Path of the directory
	path: string
	// FIXME: this is not very dev friendly, as Cue does not support octal notation.
	// What is a better option?
	mode: int
	// Create parent directories as needed?
	parents: *true | false

	output: #FS
}

#Copy: {
	_copy: {}

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

#Merge: {
	_merge: {}

	input: #FS
	layers: [...#CopyInfo]
	output: #FS
}
