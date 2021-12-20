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
		params: {
			@dagger(notimplemented)
			[name=string]: _
		}
	}

	// Send outputs to the client
	outputs: {
		@dagger(notimplemented)
		directories: [name=string]: _#outputDirectory
	}

	// Forward network services to and from the client
	proxy: [endpoint=string]: _#proxyEndpoint

	// Execute actions in containers
	actions: {
		...
	}
}

_#inputDirectory: {
	// FIXME: rename to "InputDirectory" for consistency
	$dagger: task: _name: "LocalDirectory"

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
	// Reference to the secret contents
	// Use this by securely mounting it into a container.
	// See universe.dagger.io/docker.#Run.mounts
	// FIXME: `contents` field name causes confusion (not actually the secret contents..)
	contents: #Secret

	{
		@dagger(notimplemented)

		// Execute a command ON THE CLIENT MACHINE and read secret from standard output
		command: [string, ...string] | string
		// Execute command in an interactive terminal
		//  for example to prompt for a passphrase
		interactive: true | *false
	} | {
		// Read secret from a file ON THE CLIENT MACHINE
		$dagger: task: _name: "SecretFile"
		path: string
	} | {
		// Read secret from an environment variable ON THE CLIENT MACHINE
		$dagger: task: _name: "SecretEnv"
		envvar: string
	}
}

_#outputDirectory: {
	@dagger(notimplemented)

	// Filesystem contents to export
	// Reference an #FS field produced by an action
	contents: #FS

	// Export to this path ON THE CLIENT MACHINE
	dest: string
}

// Forward a network endpoint to and from the client
_#proxyEndpoint: {
	// Service endpoint can be proxied to action containers as unix sockets
	// FIXME: should #Service be renamed to #ServiceEndpoint or #Endpoint? Naming things is hard...
	$dagger: task: _name: "Service"
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
