package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: build: dagger.#Dockerfile & {
		source: dagger.#Scratch
		// Default is to look for a Dockerfile in the context,
		// but let's declare it here.
		dockerfile: contents: #"""
			FROM alpine
			RUN sleep 10 && echo test
			"""#
	}
}

// dagger.#Plan & {
//  actions: {
//   image: alpine.#Build & {}
// 
//   // Test script
//   test: bash.#Run & {
//    input: image.output
//    command: {
//     name: "/bin/sh"
//     args: ["-c", "sleep 10 && echo test"]
//    }
//   }
//  }
// }
