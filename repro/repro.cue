package repro

base: {
	repository: #Dir

	build: #Build & {
		source:   repository
		packages: "./cmd"
		output:   "/usr/local/bin/cmd"
	}
	help: {
		steps: [#Load & {
			from: build
		}, #Exec & {
			args: ["cmd", "-h"]
		}]
	}
}

input: {
	repository: {
		steps: [{
			do:  "local"
			dir: "."
			include: []
		}]
	}
}

output: {
	help: {
		steps: [#Load & {
			from: build
		}, #Exec & {
			args: ["cmd", "-h"]
		}]
	}

	build: {
	  version: *"1.16" | string
		source: {
			steps: [#Op & #Op & {
				do:  "local"
				dir: "."
				include: []
			}]
		}

		// Packages to build
		packages: "./cmd"

		// Target architecture
		arch: *"amd64" | string

		// Target OS
		os: *"linux" | string

		// Build tags to use for building
		tags: *"" | string

		// LDFLAGS to use for linking
		ldflags: *"" | string

		// Specify the targeted binary name
		output: "/usr/local/bin/cmd"
		env: [string]: string
		steps: [#Copy & {
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
	repository: {
		steps: [#Op & {
			do:  "local"
			dir: "."
			include: []
		}]
	}
}

#Dir: steps: [...#Op]

// One operation in a script
#Op: #FetchContainer | #Exec | #Local | #Copy | #Load

#Local: {
	do:      "local"
	dir:     string
	include: [...string] | *[]
}

#Load: {
	do:   "load"
	from: _
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

	steps: [
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

	steps: [
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
