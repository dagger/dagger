package main

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		pull: core.#Pull & {
			source: "alpine"
		}
		pull315: core.#Pull & {
			source: "alpine:3.15"
		}
		exec: core.#Exec & {
			input: pull.output
			args: ["echo", "hello world", "from joel 12345"]
		}

		print_versions: {
			latest: core.#Exec & {
				input: pull.output
				args: ["cat", "/etc/alpine-release"]
			}
			"315": core.#Exec & {
				input: pull315.output
				args: ["cat", "/etc/alpine-release"]
			}
		}
		exec3: core.#Exec & {
			input: pull315.output
			args: ["cat", "/etc/alpine-release"]
		}
		exec4: core.#Exec & {
			input: pull.output
			args: ["cat", "/315/etc/alpine-release"]
			mounts: "315": {
				dest:     "/315"
				contents: pull315.output
			}
		}
		exec5: core.#Exec & {
			input: pull.output
			args: ["cat", "/etc/alpine-release"]
			mounts: "315": {
				dest:     "/315"
				contents: pull315.output
			}
		}

		exec6: core.#Exec & {
			input: pull.output
			args: ["ls"]
			workdir: "/bin"
		}

		exec2: core.#Exec & {
			input: pull.output
			env: "JOEL": "joel1234"
			args: ["printenv", "JOEL"]
		}

		git: {
			repo: core.#GitPull & {
				remote: "https://github.com/dagger/dagger"
				ref:    "main"
			}

			image: core.#Pull & {
				source: "alpine:3.15.0"
			}

			verify: core.#Exec & {
				input: image.output
				// args: ["ls", "/"]
				// args: ["cat", "/etc/alpine-release"]
				args: ["cat", "/dagger1/README.md"]
				mounts: {
					a: {dest: "/dagger1", contents: repo.output}
				}
			}
		}

		writefile: {
			w: core.#WriteFile & {
				input:    pull.output
				path:     "/joel.txt"
				contents: "writefile - hello from the radical past\n"
			}
			verify: core.#Exec & {
				input: w.output
				args: ["cat", "/joel.txt"]
			}
		}

		// dockerfile: {
		// 	w: core.#WriteFile & {
		// 		input: pull.output
		// 		path:  "/Dockerfile"
		// 		contents: """
		// 			  FROM alpine
		// 			  RUN echo 'dockerfile - hello world, from joel - for real' > joel.txt
		// 			"""
		// 	}
		// 	d: core.#Dockerfile & {
		// 		source: w.output
		// 		dockerfile: path: "/Dockerfile"
		// 	}

		// 	verify: core.#Exec & {
		// 		input: d.output
		// 		args: ["cat", "/joel.txt"]
		// 	}
		// }

		http: {
			get: core.#HTTPFetch & {
				source: "https://example.com/"
				dest:   "/example.html"
			}
			readfile: core.#ReadFile & {
				input:    get.output
				path:     "/example.html"
				contents: string
			}
			output: readfile.contents
		}

		readfile: {
			image: core.#Pull & {
				source: "alpine:3.15.0@sha256:e7d88de73db3d3fd9b2d63aa7f447a10fd0220b7cbf39803c803f2af9ba256b3"
			}

			readfile: core.#ReadFile & {
				input: image.output
				path:  "/etc/alpine-release"
			} & {
				// assert result
				contents: "3.15.0\n"
			}
		}
		copy: {
			copyRelease: core.#Copy & {
				input:    pull.output
				contents: pull315.output
				source:   "/etc/alpine-release"
				dest:     "/etc/alpine-release-3.15"
			}
			readfile1: core.#ReadFile & {
				input: copyRelease.output
				path:  "/etc/alpine-release"
			} & {
				// assert result
				contents: "3.16.2\n"
			}
			readfile2: core.#ReadFile & {
				input: copyRelease.output
				path:  "/etc/alpine-release-3.15"
			} & {
				// assert result
				contents: "3.15.6\n"
			}
		}
		push: core.#Push & {
			input: pull.output
			dest:  "localhost:5042/alpine"
		}
	}
}
