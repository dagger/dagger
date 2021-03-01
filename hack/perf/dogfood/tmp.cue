package repro

base: {
	repository: #Dir
	test: #Test & {
		source: repository
		packages: "./..."
	}
	build: #Build & {
					source:   test
					packages: "./cmd/dagger"
					output:   "/usr/local/bin/dagger"
	}
	help: {
					#dagger: {
									compute: [#Load & {
													from: build
									}, #Exec & {
													args: ["dagger", "-h"]
									}]
					}
	}
	cmd1: {
					#dagger: {
									compute: [#Load & {
													from: help
									}, #Exec & {
													args: ["dagger", "-h"]
									}]
					}
	}
	cmd2: {
					#dagger: {
									compute: [#Load & {
													from: cmd1
									}, #Exec & {
													args: ["dagger", "-h"]
									}]
					}
	}
}

input: {
	repository: {
					#dagger: {
									compute: [{
													do:  "local"
													dir: "."
													include: []
									}]
					}
	}
}

output: {
	cmd2: {
					#dagger: {
									// Run a command with the binary we just built
									compute: [#Load & {
													from: cmd1
									}, #Exec & {
													args: ["dagger", "-h"]
									}]
					}
	}

	cmd1: {
					#dagger: {
									// Run a command with the binary we just built
									compute: [#Load & {
													from: help
									}, #Exec & {
													args: ["dagger", "-h"]
									}]
					}
	}

	help: {
					#dagger: {
									// Run a command with the binary we just built
									compute: [#Load & {
													from: build
									}, #Exec & {
													args: ["dagger", "-h"]
									}]
					}
	}

	build: {
					// Go version to use
					version: *"1.16" | string

					// Source Directory to build
					// source: input.repository
					source: {
									#dagger: {
													compute: [#Op & #Op & {
																	do:  "local"
																	dir: "."
																	include: []
													}]
									}
					}

					// Packages to build
					packages: "./cmd/dagger"

					// Target architecture
					arch: *"amd64" | string

					// Target OS
					os: *"linux" | string

					// Build tags to use for building
					tags: *"" | string

					// LDFLAGS to use for linking
					ldflags: *"" | string

					// Specify the targeted binary name
					output: "/usr/local/bin/dagger"
					env: [string]: string
					#dagger: {
									compute: [#Copy & {
													from: #Go & {
																	version: version
																	"source":  source
																	"env":     env
																	args: ["build", "-v", "-tags", tags, "-ldflags", ldflags, "-o", output, packages]
													}
													src:  output
													dest: output
									}]
					}
	}
	repository: {
					#dagger: {
									compute: [#Op & {
													do:  "local"
													dir: "."
													include: []
									}]
					}
	}
}


// Any component can be referenced as a directory, since
// every dagger script outputs a filesystem state (aka a directory)
#Dir: #dagger: compute: [...#Op]

// One operation in a script
#Op: #FetchContainer | #FetchGit | #Export | #Exec | #Local | #Copy | #Load | #Subdir

// Export a value from fs state to cue
#Export: {
	do: "export"
	// Source path in the container
	source: string
	format: "json" | "yaml" | *"string"
}

#Local: {
	do:      "local"
	dir:     string
	include: [...string] | *[]
}

// FIXME: bring back load (more efficient than copy)

#Load: {
	do:   "load"
	from: _
}

#Subdir: {
	do:  "subdir"
	dir: string | *"/"
}

#Exec: {
	do: "exec"
	args: [...string]
	env?: [string]: string
	always?: true | *false
	dir:     string | *"/"
	mount: [string]: "tmp" | "cache" | {from: _, path: string | *"/"}
}

#FetchContainer: {
	do:  "fetch-container"
	ref: string
}

#FetchGit: {
	do:     "fetch-git"
	remote: string
	ref:    string
}

#Copy: {
	do:   "copy"
	from: _
	src:  string | *"/"
	dest: string | *"/"
	...
}

#DockerBuild: {
	do: "docker-build"
	// We accept either a context, a Dockerfile or both together
	context?:        _
	dockerfilePath?: string // path to the Dockerfile (defaults to "Dockerfile")
	dockerfile?:     string

	platforms?: [...string]
	buildArg?: [string]: string
	label?: [string]:    string
}

#Go: {
	// Go version to use
	version: *"1.16" | string

	// Arguments to the Go binary
	args: [...string]

	// Source Directory to build
	source: #Dir

	// Environment variables
	env: [string]: string

	#dagger: compute: [
		#FetchContainer & {
			ref: "docker.io/golang:\(version)-alpine"
		},
		#Exec & {
			"args": ["go"] + args

			"env": env
			env: CGO_ENABLED: "0"
			// FIXME: this should come from the golang image.
			// https://github.com/dagger/dagger/issues/130
			env: {
				PATH:   "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
				GOPATH: "/go"
			}

			dir: "/src"
			mount: "/src": from: source

			mount: "/root/.cache": "cache"
		},
	]
}

#Build: {
	// Go version to use
	version: *#Go.version | string

	// Source Directory to build
	source: #Dir

	// Packages to build
	packages: *"." | string

	// Target architecture
	arch: *"amd64" | string

	// Target OS
	os: *"linux" | string

	// Build tags to use for building
	tags: *"" | string

	// LDFLAGS to use for linking
	ldflags: *"" | string

	// Specify the targeted binary name
	output: string

	env: [string]: string

	#dagger: compute: [
		#Copy & {
			from: #Go & {
				"version": version
				"source":  source
				"env":     env
				args: ["build", "-v", "-tags", tags, "-ldflags", ldflags, "-o", output, packages]
			}
			src:  output
			dest: output
		},
	]
}

#Test: {
	// Go version to use
	version: *#Go.version | string

	// Source Directory to build
	source: #Dir

	// Packages to test
	packages: *"." | string

	#Go & {
		"version": version
		"source":  source
		args: ["test", "-v", packages]
	}
}
