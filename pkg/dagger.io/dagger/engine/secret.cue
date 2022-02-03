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

// Securely apply a CUE transformation on the contents of a secret
// FIXME: disabled due to data race associated with filling #function.input
// #TransformSecret: {
//  $dagger: task: _name: "TransformSecret"
//  // The original secret
//  input: #Secret
//  // A new secret or (map of secrets) with the transformation applied
//  output: #Secret | {[string]: output}
//  // Transformation function
//  #function: {
//   // Full contents of the input secret (only available to the function)
//   input:           string
//   _functionOutput: string | {[string]: _functionOutput}
//   // New contents of the output secret (must provided by the caller)
//   output: _functionOutput
//  }
// }

#DecodeSecret: {
	$dagger: task: _name: "DecodeSecret"

	// A #Secret whose plain text is a JSON or YAML string
	input: #Secret

	format: "json" | "yaml"

	// A new secret or (map of secrets) derived from unmarshaling the input secret's plain text
	output: #Secret | {[string]: output}
}
