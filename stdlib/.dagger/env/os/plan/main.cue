package main

import (
	"dagger.io/os"
	"dagger.io/alpine"
)

// Write a file to an empty dir
EmptyDir: {
	f: os.#File & {
		path: "/foo.txt"
		write: data: "hello world!"
	}
	f: contents: "hello world!"
}

// Read from a pre-existing file
Read: {
	f: os.#File & {
		from: alpine.#Image & {
			version: "3.13.4"
		}
		path: "/etc/alpine-release"
	}
	f: contents: "3.13.4\n"
}
