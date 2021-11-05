package main

import (
	"alpha.dagger.io/dagger/op"
	"alpha.dagger.io/dagger"
)

targetArch: dagger.#Input & {string}

TestFetch: #up: [
	op.#FetchContainer & {
		ref: "docker.io/alpine"
	},

	op.#Exec & {
		args: ["/bin/sh", "-c", "echo $(uname -a) >> /platform.txt"]
		always: true
	},

	op.#Exec & {
		args: ["/bin/sh", "-c", """
			cat /platform.txt | grep "$TARGET_ARCH"
			"""]
		env: TARGET_ARCH: targetArch
	},
]

TestBuild: #up: [
	op.#DockerBuild & {
		dockerfile: """
				FROM alpine
				
				RUN echo $(uname -a) > /platform.txt
			"""
	},

	op.#Exec & {
		args: ["/bin/sh", "-c", """
			cat /platform.txt | grep "$TARGET_ARCH"
			"""]
		env: TARGET_ARCH: targetArch
	},
]

TestLoad: #up: [
	op.#Load & {
		from: TestBuild
	},

	// Compare arch
	op.#Exec & {
		args: ["/bin/sh", "-c", "diff /build/platform.txt /fetch/platform.txt"]
		mount: {
			"/build": from: TestBuild
			"/fetch": from: TestFetch
		}
	},
]
