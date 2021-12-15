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
