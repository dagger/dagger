// The Dagger API.
package dagger

// A deployment plan executed by `dagger up`
#Plan: #DAG

// A special kind of program which `dagger` can execute.
#DAG: {
	// Receive inputs from the client
	input: {
		// Receive directories
		directories: [name=string]: _#inputDirectory
		// Securely receive secrets
		secrets: [name=string]: _#inputSecret
		// Receive runtime parameters
		params: [name=string]: _
	}

	// Send outputs to the client
	output: {
		directories: [name=string]: _#outputDirectory
	}

	// Forward network services to and from the client
	proxy: [name=string]: _#proxyEndpoint

	// Execute actions in containers
	actions: {
		...
	}
}

_#inputDirectory: {
	// Import from this path ON THE CLIENT MACHINE
	// Example: "/Users/Alice/dev/todoapp/src"
	source: string

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

_#outputDirectory: {
	// Filesystem contents to export
	// Reference an #FS field produced by an action
	contents: #FS

	// Export to this path ON THE CLIENT MACHINE
	dest: string
}

// Securely receive a secret from the client
_#inputSecret: {
	// Reference to the secret contents
	// Use this by securely mounting it into a container.
	// See universe.dagger.io/docker.#Run.mounts
	// FIXME: `contents` field name causes confusion (not actually the secret contents..)
	contents: #Secret

	{
		// Execute a command ON THE CLIENT MACHINE and read secret from standard output
		command: [string, ...string] | string
		// Execute command in an interactive terminal
		//  for example to prompt for a passphrase
		interactive: true | *false
	} | {
		// Read secret from a file ON THE CLIENT MACHINE
		path: string
	} | {
		// Read secret from an environment variable ON THE CLIENT MACHINE
		envvar: string
	}
}

// Forward a network endpoint to and from the client
_#proxyEndpoint: {
	// Service endpoint can be proxied to action containers as unix sockets
	// FIXME: should #Service be renamed to #ServiceEndpoint or #Endpoint? Naming things is hard...
	endpoint: #Service

	{
		// Listen for connections ON THE CLIENT MACHINE, proxy to actions
		listen: #Address
	} | {
		// Connect to a remote endpoint FROM THE CLIENT MACHINE, proxy to actions
		connect: #Address
	} | {
		// Proxy to/from the contents of a file ON THE CLIENT MACHINE
		filepath: string
	} | {
		// Proxy to/from standard input and output of a command ON THE CLIENT MACHINE
		command: [string, ...string] | string
	}
}
