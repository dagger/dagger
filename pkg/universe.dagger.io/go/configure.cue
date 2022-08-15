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

	// path to go root in the specified filesystem (be it an image ref or a dagger.#FS)
	source: string | *#DefaultGoEnvs.GOROOT

	_cfg: _#Configure & {
		"input":  input
		"source": source
		"goroot": goroot
		"gopath": gopath
	}

	{
		// filesystem with go in it.
		// This will be copied into the image at the path specified by `goroot`
		// if `source` is specified just the path to the source will be copied.
		contents: dagger.#FS
		_cfg:     _#Configure & {
			"contents": contents
		}
	} | {
		// ref to an image with go in it.
		// e.g. docker.io/library/golang:latest
		ref:   core.#Ref
		_pull: docker.#Pull & {
			source: ref
		}
		_cfg: _#Configure & {
			contents: _pull.output.rootfs
		}
	}

	output: _cfg.output
}

_#Configure: {
	input:    docker.#Image
	contents: dagger.#FS
	goroot:   string
	gopath:   string
	source:   string

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

	_set: docker.#Set & {
		"input": docker.#Image & {
			rootfs: _merge.output
			config: input.config
		}
		config: core.#ImageConfig & {
			env: {
				GOROOT: goroot
				GOPATH: gopath
				PATH:   "\(gopath)/bin:\(goroot)/bin:\(input.config.env.PATH)"
			}
		}
	}
	output: _set.output
}
