package engine

// Create a new a secret from a filesystem tree
#NewSecret: {
	$dagger: task: _name: "NewSecret"

	// Filesystem tree holding the secret
	input: #FS
	// Path of the secret to read
	path: string
	// Whether to trim leading and trailing space characters from secret value
	trimSpace: *true | false
	// Contents of the secret
	output: #Secret
}
