package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: build: dagger.#Build & {
		steps: [
			docker.#Pull & {
				source: "alpine"
			},
			docker.#Run & {
				command: {
					name: "sh"
					args: ["-c", "sleep 10 && echo -n test > /test"]
				}
				user: "root"
			},
		]
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
