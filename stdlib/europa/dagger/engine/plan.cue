package engine

// A deployment plan executed by `dagger up`
#Plan: {
	// Receive inputs from the client
	input: {
		// Receive directories
		directories: [string]: _#inputDirectory
		// Securely receive secrets
		secrets: [string]: _#inputSecret
	}

	// Forward network services to and from the client
	proxy: [string]: _#proxyEndpoint

	// Execute actions in containers
	actions: {
		...
	}
}

_#inputDirectory: {
	// Import from this path ON THE CLIENT MACHINE
	// Example: "/Users/Alice/dev/todoapp/src"
	_type: "LocalDirectory"
	path:  string

	// Filename patterns to include
	// Example: ["*.go", "Dockerfile"]
	include?: [...string]

	// Filename patterns to exclude
	// Example: ["node_modules"]
	exclude?: [...string]

	// Imported filesystem contents
	// Use this as input for actions requiring an #FS field
	contents: #FS
}

// Securely receive a secret from the client
_#inputSecret: {
	// Reference to the secret contents
	// Use this by securely mounting it into a container.
	// See universe.dagger.io/docker.#Run.mounts
	// FIXME: `contents` field name causes confusion (not actually the secret contents..)
	contents: #Secret

	{
		// Read secret from a file ON THE CLIENT MACHINE
		_type: "SecretFile"
		path:  string
	} | {
		// Read secret from an environment variable ON THE CLIENT MACHINE
		_type:  "SecretEnv"
		envvar: string
	}
}

// Forward a network endpoint to and from the client
_#proxyEndpoint: {
	// Service endpoint can be proxied to action containers as unix sockets
	// FIXME: should #Service be renamed to #ServiceEndpoint or #Endpoint? Naming things is hard...
	// FIXME: reconcile with spec
	_type:   "Service"
	service: #Service
	{
		unix: string
	} | {
		npipe: string
	}
}
