package engine

// Produce an empty directory
// FIXME: replace with a null value for #FS?
#Scratch: {
	$dagger: task: _name: "Scratch"

	output: #FS
}

#ReadFile: $dagger: task: _name: "ReadFile"

#WriteFile: $dagger: task: _name: "WriteFile"

// Create a directory
#Mkdir: {
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
