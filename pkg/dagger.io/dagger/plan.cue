package dagger

// A special kind of program which `dagger` can execute.
#Plan: {
	// Access client machine
	client: {
		// Access client filesystem
		// Path may be absolute, or relative to client working directory
		filesystem: [path=string]: {
			// Read data from that path
			read?: _#clientFilesystemRead & {
				"path": path
			}

			// If set, Write to that path
			write?: _#clientFilesystemWrite & {
				"path": path

				// avoid race condition
				if read != _|_ {
					_after: read
				}
			}
		}

		// Access client environment variables
		env: [string]: *string | #Secret

		// Execute commands in the client
		commands: [id=string]: _#clientCommand

		// Platform of the client machine
		platform: _#clientPlatform
	}

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

_#clientFilesystemRead: {
	$dagger: task: _name: "ClientFilesystemRead"

	// Path may be absolute, or relative to client working directory
	path: string

	{
		// CUE type defines expected content:
		//     string: contents of a regular file
		//     #Secret: secure reference to the file contents
		contents: string | #Secret
	} | {
		// CUE type defines expected content:
		//     #FS: contents of a directory
		contents: #FS

		// Filename patterns to include
		// Example: ["*.go", "Dockerfile"]
		include?: [...string]

		// Filename patterns to exclude
		// Example: ["node_modules"]
		exclude?: [...string]
	} | {
		// CUE type defines expected content:
		//     #Service: unix socket or npipe
		contents: #Service

		// Type of service
		type: *"unix" | "npipe"
	}
}

_#clientFilesystemWrite: {
	$dagger: task: _name: "ClientFilesystemWrite"

	// Path may be absolute, or relative to client working directory
	path: string
	{
		// File contents to export (as a string or secret)
		contents: string | #Secret

		// File permissions (defaults to 0o644)
		permissions?: int
	} | {
		// Filesystem contents to export
		// Reference an #FS field produced by an action
		contents: #FS
	}
}

_#clientCommand: {
	$dagger: task: _name: "ClientCommand"

	name: string
	args: [...string]
	flags: [string]: bool | string
	env: [string]:   string | #Secret

	// Capture standard output (as a string or secret)
	stdout?: *string | #Secret

	// Capture standard error (as a string or secret)
	stderr?: *string | #Secret

	// Inject standard input (from a string or secret)
	stdin?: string | #Secret
}

_#clientPlatform: {
	$dagger: task: _name: "ClientPlatform"

	// Operating system of the client machine
	os: string
	// Hardware architecture of the client machine
	arch: string
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
	// See universe.io/docker.#Run.mounts
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
