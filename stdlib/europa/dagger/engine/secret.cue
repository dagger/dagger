package engine

// Load a secret from a filesystem tree
#LoadSecret: {
	$dagger: task: _name: "LoadSecret"

	// Filesystem tree holding the secret
	input: #FS
	// Path of the secret to read
	path: string
	// Whether to trim leading and trailing space characters from secret value
	trimSpace: *true | false
	// Contents of the secret
	contents: #Secret
}
