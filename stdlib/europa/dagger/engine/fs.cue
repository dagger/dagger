package engine

#ReadFile: {
	_type: "ReadFile"

	input:    #FS
	path:     string
	contents: string
	output:   #FS
}

#WriteFile: {
	_type: "WriteFile"

	input:    #FS
	path:     string
	contents: string
	mode:     int
	output:   #FS
}
