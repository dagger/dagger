package dagger

import (
	"alpha.dagger.io/dagger/llb2"
)

// A deployment plan executed by `dagger up`
#Plan: {
	context: #Context
	actions: {
		...
	}
}

#Context: {
	// Import directories
	import: [name=string]: {
		llb2.#Import

		path: string
		include?: [...string]
		exclude?: [...string]
	}

	// Export directories
	export: [name=string]: {
		llb2.#FS

		path: string
	}

	// Securely load external secrets
	secrets: [name=string]: {
		llb2.#Secret

		{
			// Execute a command and read secret from standard output
			command: [string, ...string] | string
		} | {
			// Read secret from a file
			path: string
		} | {
			// Read secret from an environment variable
			envvar: string
		}
	}

	// Consume and publish network services
	services: [name=string]: {
		llb2.#Service

		_#Address: string & =~"^(tcp://|unix://|udp://).*"
		{
			// Listen for connections on the client, proxy to actions
			listen: _#Address
		} | {
			// Connect to a remote endpoint, proxy to actions
			connect: _#Address
		} | {
			// Proxy to/from the contents of a file
			filepath: string
		} | {
			// Proxy to/from standard input and output of a command
			command: [string, ...string] | string
		}
	}
}
