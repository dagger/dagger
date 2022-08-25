package jreleaser

import (
	"dagger.io/dagger"
)

#JReleaserCommand: "download" | "assemble" | "changelog" | "checksum" | "sign" | "upload" | "release" | "prepare" | "package" | "publish" | "announce" | "full-release"

// Base command
_#command: {
	// --== Public ==--

	// Source code
	source: dagger.#FS

	// JReleaser home path
	jreleaser_home?: dagger.#FS

	// JReleaser version
	version: string | *"latest"

	// JReleaser command to be executed
	cmd: #JReleaserCommand

	// Additional command arguments
	args: [...string]

	// Additional command flags
	flags: [string]: (string | true)

	// Environment variables
	env: [string]: string | dagger.#Secret

	_container: #Container & {
		"jreleaser_home": jreleaser_home
		"source":         source
		"version":        version
		"cmd":            cmd
		"args":           args
		"flags":          flags
		"env":            env
		export: {
			directories: "/out/jreleaser": dagger.#FS
			files: {
				"/out/jreleaser/trace.log":         string
				"/out/jreleaser/output.properties": string
			}
		}
	}

	// --== Outputs ==--

	output:      _container.output
	outputDir:   _container.export.directories["/out/jreleaser"]
	outputLog:   _container.export.files["/out/jreleaser/trace.log"]
	outputProps: _container.export.files["/out/jreleaser/output.properties"]
}

#Download: _#command & {
	cmd: "download"
}

#Assemble: _#command & {
	cmd: "assemble"
}

#Changelog: _#command & {
	cmd: "changelog"
}

#Checksum: _#command & {
	cmd: "checksum"
}

#Sign: _#command & {
	cmd: "sign"
}

#Upload: _#command & {
	cmd: "upload"
}

#Release: _#command & {
	cmd: "release"
}

#Prepare: _#command & {
	cmd: "prepare"
}

#Package: _#command & {
	cmd: "package"
}

#Publish: _#command & {
	cmd: "publish"
}

#Announce: _#command & {
	cmd: "announce"
}

#FullRelease: _#command & {
	cmd: "full-release"
}
