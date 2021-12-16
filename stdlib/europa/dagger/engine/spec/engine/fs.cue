package engine

// A filesystem state
#FS: {
	$dagger: fs: _id: string
}

// Produce an empty directory
// FIXME: replace with a null value for #FS?
#Scratch: {
	$dagger: task: _name: "Scratch"

	output: #FS
}

#ReadFile: {
	$dagger: task: _name: "ReadFile"

	input:    #FS
	path:     string
	contents: string
	output:   #FS
}

#WriteFile: {
	$dagger: task: _name: "WriteFile"

	input:    #FS
	path:     string
	contents: string
	output:   #FS
}

// Create a directory
#Mkdir: {
	$dagger: task: _name: "Mkdir"

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

#Merge: {
	$dagger: task: _name: "Merge"

	input: #FS
	layers: [...#CopyInfo]
	output: #FS
}
