package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
)

// Default values used for #Configure.
#DefaultGoEnvs: {
	GOROOT: "/usr/local/go"
	GOPATH: "/go"
}

// Configure installs go into the given input image.
// It sets GOROOT, GOPATH, and adds the relevent go bin paths to PATH.
#Configure: {
	input: docker.#Image
	// path to install go to in `input` and set the GOROOT env var to
	// In most cases you want to use the default value provided here.
	goroot: string | *#DefaultGoEnvs.GOROOT
	// path for GOPATH to be set to
	// In most cases you want to use the default value provided here.
	gopath: string | *#DefaultGoEnvs.GOPATH

	// filesystem with go in it.
	// This will be copied into the image at the path specified by `goroot`
	// if `source` is specified just the path to the source will be copied.
	contents: dagger.#FS
	// path to go root in the `contents` filesystem
	source: string | *#DefaultGoEnvs.GOROOT

	docker.#Build & {
		steps: [
			{output: input},
			{
				input:   docker.#Image
				_subDir: core.#Subdir & {
					input: contents
					path:  "\(source)"
				}

				_goWithRoot: core.#Copy & {
					input:    dagger.#Scratch
					contents: _subDir.output
					dest:     "\(goroot)"
				}

				_merge: core.#Merge & {
					inputs: [input.rootfs, _goWithRoot.output]
				}
				output: docker.#Image & {
					config: input.config
					rootfs: _merge.output
				}
			},
			docker.#Set & {
				input:  docker.#Image
				config: core.#ImageConfig & {
					env: {
						GOROOT: goroot
						GOPATH: gopath
						PATH:   "\(gopath)/bin:\(goroot)/bin:\(input.config.env.PATH)"
					}
				}
			},
		]
	}
}
