package dagger

// Push a directory to a git remote
#GitPush: {
	// Reserved for runtime use
	_gitPushID: string

	remote: string
	ref: string
	contents: #FS
}

// Pull a directory from a git remote
#GitPull: {
	// Reserved for runtime use
	_gitPullID: string

	remote: string
	ref: string
	contents: #FS
}
