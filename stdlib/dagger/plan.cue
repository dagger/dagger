package dagger


// A deployment plan executed by `dagger up`
#Plan: {
	title: string
	description?: string

	context: {
		directories: [name=string]: {
			"name": name
			description: string
			path: string
			include?: [...string]
			exclude?: [...string]

			// Can be copied/mounted into actions
			data: #LocalDir
		}

		secrets: [name=string]: {
			"name": name
			description: string

			{
				// Execute a command and read secret from standard output
				cmd: [string, ...string] | string
			} | {
				// Read secret from a file
				path: string
			} | {
				// Read secret from an environment variable
				envvar: string
			}

			// Can be securely mounted into actions
			data: #Secret
		}

		services: [name=string]: {
			"name": name
			description: string

			{
				// Listen for connections on the client, proxy to actions
				listen: #ServiceAddress
			} | {
				// Connect to a remote endpoint, proxy to actions
				connect: #ServiceAddress
			} | {
				// Proxy to/from the contents of a file
				file: string
			} | {
				// Proxy to/from standard input and output of a command
				cmd: [string, ...string] | string
			}

			// Can be proxied to actions as a unix socket or (coming soon) tcp endpoint
			data: #Service
		}
	}

	actions: {
		...
	}
}


#ServiceAddress: string & =~ "^(tcp://|unix://|udp://).*"
