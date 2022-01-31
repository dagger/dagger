package engine

// A deployment plan executed by `dagger up`
#Plan: #DAG

// A special kind of program which `dagger` can execute.
#DAG: {
	// Receive inputs from the client
	inputs: {
		// Receive directories
		directories: [name=string]: _#inputDirectory
		// Securely receive secrets
		secrets: [name=string]: _#inputSecret
		// Receive runtime parameters
		params: [name=string]: _
	}

	// Send outputs to the client
	outputs: {
		// Export an #FS to the client
		directories: [name=string]: _#outputDirectory
		// Export a string to a file
		files: [name=string]: _#outputFile
	}

	// Forward network services to and from the client
	proxy: [endpoint=string]: _#proxyEndpoint

	// Configure platform execution
	platform?: string

	// Execute actions in containers
	actions: {
		...
	}
}

_#inputDirectory: {
	// FIXME: rename to "InputDirectory" for consistency
	$dagger: task: _name: "InputDirectory"

	// Import from this path ON THE CLIENT MACHINE
	// Example: "/Users/Alice/dev/todoapp/src"
	path: string

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
	_#inputSecretEnv | _#inputSecretFile | _#inputSecretExec

	// Reference to the secret contents
	// Use this by securely mounting it into a container.
	// See universe.dagger.io/docker.#Run.mounts
	// FIXME: `contents` field name causes confusion (not actually the secret contents..)
	contents: #Secret

	// Whether to trim leading and trailing space characters from secret value
	trimSpace: *true | false
}

// Read secret from an environment variable ON THE CLIENT MACHINE
_#inputSecretEnv: {
	$dagger: task: _name: "InputSecretEnv"

	envvar: string

	contents: #Secret
}

// Read secret from a file ON THE CLIENT MACHINE
_#inputSecretFile: {
	$dagger: task: _name: "InputSecretFile"

	path: string

	contents: #Secret
}

// Get secret by executing a command ON THE CLIENT MACHINE
_#inputSecretExec: {
	$dagger: task: _name: "InputSecretExec"

	command: {
		name: string
		args: [...string]
		interactive: true | *false @dagger(notimplemented) // FIXME: https://github.com/dagger/dagger/issues/1268
	}

	contents: #Secret
}

_#outputDirectory: {
	$dagger: task: _name: "OutputDirectory"

	// Filesystem contents to export
	// Reference an #FS field produced by an action
	contents: #FS

	// Export to this path ON THE CLIENT MACHINE
	dest: string
}

_#outputFile: {
	$dagger: task: _name: "OutputFile"

	// File contents to export
	contents: string

	// Export to this path ON THE CLIENT MACHINE
	dest: string

	// Permissions of the file (defaults to 0o644)
	permissions?: int
}

// Forward a network endpoint to and from the client
_#proxyEndpoint: {
	$dagger: task: _name: "ProxyEndpoint"

	// Service endpoint can be proxied to action containers as unix sockets
	// FIXME: should #Service be renamed to #ServiceEndpoint or #Endpoint? Naming things is hard...
	// FIXME: should be endpoint
	service:  #Service
	endpoint: service
	{
		// FIXME: reconcile with spec
		unix: string
	} | {
		// FIXME: reconcile with spec
		npipe: string
	} | {
		// Listen for connections ON THE CLIENT MACHINE, proxy to actions
		listen: #Address @dagger(notimplemented)
	} | {
		// Connect to a remote endpoint FROM THE CLIENT MACHINE, proxy to actions
		connect: #Address @dagger(notimplemented)
	} | {
		// Proxy to/from the contents of a file ON THE CLIENT MACHINE
		filepath: string @dagger(notimplemented)
	} | {
		// Proxy to/from standard input and output of a command ON THE CLIENT MACHINE
		command: [string, ...string] | string @dagger(notimplemented)
	}
}

// A network service address
#Address: string & =~"^(tcp://|unix://|udp://).*"
