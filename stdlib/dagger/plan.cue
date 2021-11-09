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
		path: string
		include?: [...string]
		exclude?: [...string]
		fs: llb2.#Import
	}

	// Export directories
	export: [name=string]: {
		source: llb2.#FS
		path:   string
	}

	// Securely load external secrets
	secrets: [name=string]: {
		// Secrets can be securely mounted into action containers as a file
		file: llb2.#Secret

		{
			// Execute a command and read secret from standard output
			command:     [string, ...string] | string
			// Execute command in an interactive terminal (eg. to prompt user for passphrase)
			interactive: true | *false
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
		// Service endpoint can be proxied to action containers as unix sockets
		endpoint: llb2.#Service

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
