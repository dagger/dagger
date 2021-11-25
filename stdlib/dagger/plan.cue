package dagger

// A deployment plan executed by `dagger up`
#Plan: {
	context: #Context
	actions: [string]: _
}

// FIXME: Platform spec here
#Platform: string

#Context: {
	// Platform to target
	platform?: #Platform

	// Import directories
	imports: [string]: {
		_type: "Import"

		path: string
		include?: [...string]
		exclude?: [...string]
		fs: #Artifact
	}

	// Securely load external secrets
	secrets: [string]: {
		// Secrets can be securely mounted into action containers as a file
		contents: #Secret

		{
			_type: "SecretFile"
			// Read secret from a file
			path: string
		} | {
			_type: "SecretEnv"
			// Read secret from an environment variable ON THE CLIENT MACHINE
			envvar: string
		}
	}
}
