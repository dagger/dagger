package engine

// Push a directory to a git remote
#GitPush: {
	gitPush: {}

	input:  #FS
	remote: string
	ref:    string
}

// Pull a directory from a git remote
#GitPull: {
	gitPull: {}

	remote: string
	ref:    string
	output: #FS
}
