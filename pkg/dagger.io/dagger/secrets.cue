package dagger

// Decode the contents of a secrets without leaking it.
// Supported formats: json, yaml
#DecodeSecret: {
	$dagger: task: _name: "DecodeSecret"

	// A #Secret whose plain text is a JSON or YAML string
	input: #Secret

	format: "json" | "yaml"

	// A new secret or (map of secrets) derived from unmarshaling the input secret's plain text
	output: #Secret | {[string]: output}
}

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

// Trim leading and trailing space characters from a secret
#TrimSecret: {
	$dagger: task: _name: "TrimSecret"

	// Original secret
	input: #Secret

	// New trimmed secret
	output: #Secret
}
