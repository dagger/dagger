package engine

#ReadFile: {
	_type: "ReadFile"

	input:    #FS
	path:     string
	contents: string
	output:   #FS
}
