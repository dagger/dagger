package engine

// Securely apply a CUE transformation on the contents of a secret
#TransformSecret: {
	$dagger: task: _name: "TransformSecret"
	// The original secret
	input: #Secret
	// A new secret with the transformation applied
	output: #Secret
	// Transformation function
	#function: {
		// Full contents of the input secret (only available to the function)
		input: string
		// New contents of the output secret (must provided by the caller)
		output: string
	}
}
