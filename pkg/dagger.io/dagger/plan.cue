package dagger

import (
	"strings"
)

// A special kind of program which `dagger` can execute.
#Plan: {
	// Access client machine
	client: {
		// Access client filesystem
		// Path may be absolute, or relative to client working directory
		filesystem: [path=string]: {
			// Read data from that path
			read?: _#clientFilesystemRead & {
				"path": string | *path
			}

			// If set, Write to that path
			write?: _#clientFilesystemWrite & {
				"path": string | *path

				// if we read and write to the same path, under the same key,
				// assume we want to make an update
				if (read.path & write.path) != _|_ {
					_after: read
				}
			}
		}

		// Access client network endpoints
		network: [address=string]: _#clientNetwork & {
			"address": _#address | *address
		}

		// Access client environment variables
		env: _#clientEnv

		// Execute commands in the client
		commands: [id=string]: _#clientCommand

		// Platform of the client machine
		platform: _#clientPlatform
	}

	// Configure platform execution
	// FIXME: temporarily disabled
	// platform?: string

	cache?: #CacheConfig

	// Execute actions in containers
	actions: _
}

#CacheConfig: {
	$dagger: task: _name: "CacheConfig"

	// TODO: what if we want priority... should we use a list?
	imports?: [name=string]: {
		type: string
		{
			type:     "registry"
			ref:      string
			asString: "type=registry,ref=\(ref)"
		} | {
			type:     "gha"
			scope?:   string
			url?:     string
			token?:   #Secret
			asString: "type=gha,scope=\(scope)"
		}
	}

	export?: {
		type: string
		mode: *"min" | "max"
		{
			type:     "registry"
			ref:      string
			asString: "type=registry,mode=\(mode),ref=\(ref)"
		} | {
			type:     "gha"
			scope?:   string
			url?:     string
			token?:   #Secret
			asString: "type=gha,mode=\(mode),scope=\(scope)"
		}
	}
}

// TODO: move elsewhere
#GHAConfig: {
	// TODO: accept a generic struct that matches github event type so just that file/string can be provided instead of all these fields
	repository:  string
	eventName:   string
	eventNumber: string
	ref:         string
	mainBranch:  string | *"main"
	mode:        *"min" | "max"

	_mainScope: "\(repository)-\(mainBranch)"
	_prScope:   "\(repository)-\(eventNumber)"

	_mainImport: "gha-\(_mainScope)": {
		type:  "gha"
		scope: _mainScope
	}
	_prImport: "gha-\(_prScope)": {
		type:  "gha"
		scope: _prScope
	}
	_mainExport: {
		type:  "gha"
		mode:  mode
		scope: _mainScope
	}
	_prExport: {
		type:  "gha"
		mode:  mode
		scope: _prScope
	}

	output: #CacheConfig & {
		if (eventName == "push") && (ref == "refs/heads/\(mainBranch)") {
			imports: {
				_mainImport
			}
			export: _mainExport
		}
		if (eventName == "pull_request") {
			imports: {
				_mainImport
				_prImport
			}
			export: _prExport
		}
	}
}

// TODO: better name?
#FromCacheEnv: {
	imports?: string
	export?:  string
	output:   #CacheConfig & {
		if imports != _|_ {
			"imports": {
				for config in strings.Split(imports, " ") {
					"\(config)": {
						for kv in strings.Split(config, ",") {
							let split = strings.Split(kv, "=")
							"\(split[0])": split[1]
						}
					}
				}
			}
		}
		if export != _|_ {
			"export": {
				for kv in strings.Split(export, ",") {
					let split = strings.Split(kv, "=")
					"\(split[0])": split[1]
				}
			}
		}
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
		contents: {
			@dagger(generated)
			string | #Secret
		}
	} | {
		// CUE type defines expected content:
		//     #FS: contents of a directory
		contents: #FS @dagger(generated)

		// Filename patterns to include
		// Example: ["*.go", "Dockerfile"]
		include?: [...string]

		// Filename patterns to exclude
		// Example: ["node_modules"]
		exclude?: [...string]
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

_#clientNetwork: {
	$dagger: task: _name: "ClientNetwork"

	// URL to the socket
	// Example: unix:///var/run/docker.sock
	address: _#address

	{
		// unix socket or npipe
		connect: #Socket @dagger(generated)
		// } | {
		//  // FIXME: not yet implemented
		//  listen: #Socket
	}
}

// A network socket address
_#address: string & =~"^(unix://|npipe://).+"

_#clientEnv: {
	$dagger: task: _name: "ClientEnv"

	// CUE type defines expected content
	[!~"\\$dagger"]: {
		@dagger(generated)
		*string | #Secret
	}
}

_#clientCommand: {
	$dagger: task: _name: "ClientCommand"

	// Name of the command to execute
	// Examples: "ls", "/bin/bash"
	name: string

	// Positional arguments to the command
	// Examples: ["/tmp"]
	args: [...string]

	// Command-line flags represented in a civilized form
	// Example: {"-l": true, "-c": "echo hello world"}
	flags: [string]: bool | string

	// Environment variables
	// Example: {"DEBUG": "1"}
	env: [string]: string | #Secret

	// Capture standard output (as a string or secret)
	stdout?: {
		@dagger(generated)
		*string | #Secret
	}

	// Capture standard error (as a string or secret)
	stderr?: {
		@dagger(generated)
		*string | #Secret
	}

	// Inject standard input (from a string or secret)
	stdin?: {
		@dagger(generated)
		string | #Secret
	}
}

_#clientPlatform: {
	$dagger: task: _name: "ClientPlatform"

	// Operating system of the client machine
	os: string @dagger(generated)
	// Hardware architecture of the client machine
	arch: string @dagger(generated)
}
