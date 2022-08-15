package go

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"universe.dagger.io/go"
	"universe.dagger.io/docker"
	"universe.dagger.io/alpine"
)

dagger.#Plan & {
	actions: test: {
		_imgRef: "index.docker.io/golang:1.18-alpine"
		_goImg:  docker.#Pull & {
			source: _imgRef
		}

		simple: {
			_gopath: "/go"

			_build: docker.#Build & {
				steps: [
					alpine.#Build & {},
					go.#Configure & {
						contents: _goImg.output.rootfs
					},
				]
			}

			verify: docker.#Run & {
				input: _build.output
				command: {
					name: "/bin/sh"
					args: ["-c", """
							set -e
							go version | grep "1.18"
							go env GOROOT | grep '\(go.#DefaultGoEnvs.GOROOT)'
							go env GOPATH | grep '\(go.#DefaultGoEnvs.GOPATH)'
							echo $PATH | grep -E '^\(go.#DefaultGoEnvs.GOROOT)/bin:|:\(go.#DefaultGoEnvs.GOROOT)/bin:'
							echo $PATH | grep -E '^\(go.#DefaultGoEnvs.GOPATH)/bin:|:\(go.#DefaultGoEnvs.GOPATH)/bin:'
						"""]
				}
			}
		}
		custom: {
			_customGoroot: "/usr/local/gocustom"
			_customGopath: "/gocustom"

			_goContents: core.#Subdir & {
				input: _goImg.output.rootfs
				path:  go.#DefaultGoEnvs.GOROOT
			}
			_build: docker.#Build & {
				steps: [
					alpine.#Build & {},
					go.#Configure & {
						contents: _goContents.output
						source:   "/"
						goroot:   _customGoroot
						gopath:   _customGopath
					},
				]
			}

			verify: docker.#Run & {
				input: _build.output
				command: {
					name: "/bin/sh"
					args: ["-c", """
							set -e
							go version | grep "1.18"
							go env GOROOT | grep '\(_customGoroot)'
							go env GOPATH | grep '\(_customGopath)'
							echo $PATH | grep -E '^\(_customGoroot)/bin:|:\(_customGoroot)/bin:'
							echo $PATH | grep -E '^\(_customGopath)/bin:|:\(_customGopath)/bin:'
						"""]
				}
			}
		}

		ref: {
			_build: docker.#Build & {
				steps: [
					alpine.#Build & {},
					go.#Configure & {
						ref: _imgRef
					},
				]
			}
			verify: docker.#Run & {
				input: _build.output
				command: {
					name: "/bin/sh"
					args: ["-c", """
							set -e
							go version | grep "1.18"
							go env GOROOT | grep '\(go.#DefaultGoEnvs.GOROOT)'
							go env GOPATH | grep '\(go.#DefaultGoEnvs.GOPATH)'
							echo $PATH | grep -E '^\(go.#DefaultGoEnvs.GOROOT)/bin:|:\(go.#DefaultGoEnvs.GOROOT)/bin:'
							echo $PATH | grep -E '^\(go.#DefaultGoEnvs.GOPATH)/bin:|:\(go.#DefaultGoEnvs.GOPATH)/bin:'
						"""]
				}
			}
		}
	}
}
