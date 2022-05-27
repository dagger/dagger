package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"

	"universe.dagger.io/go"
)

dagger.#Plan & {
	client: filesystem: output: write: contents: actions.buildhello.output

	actions: buildhello: {
		_source: core.#WriteFile & {
			input: dagger.#Scratch
			path:  "/helloworld.go"
			contents: """
				package main
				import "fmt"
				func main() {
				  fmt.Println("Hello, World!")
				}
				"""
		}
		go.#Build & {
			source:  _source.output
			package: "/src/helloworld.go"
		}
	}
}
