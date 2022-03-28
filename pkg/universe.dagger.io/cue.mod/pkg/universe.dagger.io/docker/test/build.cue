package docker

import (
	"dagger.io/dagger"

	"universe.dagger.io/alpine"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: test: build: {
		// Test: simple docker.#Build
		simple: {
			#testValue: "hello world"

			image: docker.#Build & {
				steps: [
					alpine.#Build,
					docker.#Run & {
						command: {
							name: "sh"
							flags: "-c": "echo -n $TEST >> /test.txt"
						}
						env: TEST: #testValue
					},
				]
			}

			verify: dagger.#ReadFile & {
				input: image.output.rootfs
				path:  "/test.txt"
			}
			verify: contents: #testValue
		}

		// Test: docker.#Build with multiple steps
		multiSteps: {
			image: docker.#Build & {
				steps: [
					alpine.#Build,
					docker.#Run & {
						command: {
							name: "sh"
							flags: "-c": "echo -n hello > /bar.txt"
						}
					},
					docker.#Run & {
						command: {
							name: "sh"
							flags: "-c": "echo -n $(cat /bar.txt) world > /foo.txt"
						}
					},
					docker.#Run & {
						command: {
							name: "sh"
							flags: "-c": "echo -n $(cat /foo.txt) >> /test.txt"
						}
					},
				]
			}

			verify: dagger.#ReadFile & {
				input: image.output.rootfs
				path:  "/test.txt"
			}
			verify: contents: "hello world"
		}

		// Test: simple nesting of docker.#Build
		nested: {
			build: docker.#Build & {
				steps: [
					docker.#Build & {
						steps: [
							docker.#Pull & {
								source: "alpine"
							},
							docker.#Run & {
								command: name: "ls"
							},
						]
					},
					docker.#Run & {
						command: name: "ls"
					},
				]
			}
		}

		// Test: nested docker.#Build with 3+ levels of depth
		// FIXME: this test currently fails.
		nestedDeep: {
			//   build: docker.#Build & {
			//    steps: [
			//     docker.#Build & {
			//      steps: [
			//       docker.#Build & {
			//        steps: [
			//         docker.#Pull & {
			//          source: "alpine"
			//         },
			//         docker.#Run & {
			//          command: name: "ls"
			//         },
			//        ]
			//       },
			//       docker.#Run & {
			//        command: name: "ls"
			//       },
			//      ]
			//     },
			//     docker.#Run & {
			//      command: name: "ls"
			//     },
			//    ]
			//   }
		}
	}
}
