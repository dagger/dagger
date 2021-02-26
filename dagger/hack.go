package dagger

import (
	"fmt"
	"path"

	cueload "cuelang.org/go/cue/load"

	"dagger.io/go/dagger/compiler"
	"dagger.io/go/stdlib"
)

func Hack(args []string) error {
	fmt.Println("Hack", args)



	// ===========  Setup from mergeValues
	var (
		state     = compiler.EmptyStruct()
		stateInst = state.CueInst()
		err       error
	)
	env, err := NewEnv()
	if err != nil {
		return fmt.Errorf("unable to initialize env: %w", err)
	}



	// ===========  CueBuild setup
	buildConfig := &cueload.Config{
		// The CUE overlay needs to be prefixed by a non-conflicting path with the
		// local filesystem, otherwise Cue will merge the Overlay with whatever Cue
		// files it finds locally.
		Dir: "/config",
	}
	// Start by creating an overlay with the stdlib
	buildConfig.Overlay, err = stdlib.Overlay(buildConfig.Dir)
	if err != nil {
		return fmt.Errorf("unable to add stdlib: %w", err)
	}

	buildConfig.Overlay[path.Join(buildConfig.Dir, "empty.cue")] = cueload.FromBytes([]byte(""))



	// =========== infamous 3-values load

	// buildConfig.Overlay[path.Join(buildConfig.Dir, "input.cue")] = cueload.FromBytes([]byte(baseVal))
	env.base, err = compiler.Compile("base", baseVal)
	if err != nil {
		return fmt.Errorf("while compiling baseVal: %w", err)
	}

	env.input, err = compiler.Compile("input", inputVal)
	if err != nil {
		return fmt.Errorf("while compiling inputVal: %w", err)
	}

	env.output, err = compiler.Compile("output", outputVal)
	if err != nil {
		return fmt.Errorf("while compiling outputVal: %w", err)
	}



	// =========== processing from mergeValues


	fmt.Println(env.base.Cue())
	fmt.Println(env.input.Cue())
	fmt.Println(env.output.Cue())

	stateInst, err = stateInst.Fill(env.base.Cue())
	if err != nil {
		return fmt.Errorf("merge base & input: %w", err)
	}

	fmt.Println("env.mergeState.1")

	stateInst, err = stateInst.Fill(env.input.Cue())
	if err != nil {
		return fmt.Errorf("merge base & input: %w", err)
	}

	fmt.Println("env.mergeState.2")

	stateInst, err = stateInst.Fill(env.output.Cue())
	if err != nil {
		return fmt.Errorf("merge output with base & input: %w", err)
	}

	fmt.Println("env.mergeState.3")
	mv := stateInst.Value()
	fmt.Println("env.mergeState.4")

	fmt.Println(mv)

	return nil
}

const baseVal = `
repository: dagger.#Dir

build: go.#Build & {
				source:   repository
				packages: "./cmd/dagger"
				output:   "/usr/local/bin/dagger"
}
help: {
				#dagger: {
								compute: [dagger.#Load & {
												from: build
								}, dagger.#Exec & {
												args: ["dagger", "-h"]
								}]
				}
}

`

const inputVal = `
repository: {
				#dagger: {
								compute: [{
												do:  "local"
												dir: "."
												include: []
								}]
				}
}
`

const outputVal = `
help: {
				#dagger: {
								// Run a command with the binary we just built
								compute: [dagger.#Load & {
												from: build
								}, dagger.#Exec & {
												args: ["dagger", "-h"]
								}]
				}
}

build: {
				// Go version to use
				// version: *#Go.version | string
				version: *"1.16" | string

				// Source Directory to build
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
				env: {}
				#dagger: {
								compute: [dagger.#Copy & {
												from: #Go & {
																version: version
																source:  source
																env:     env
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
`
