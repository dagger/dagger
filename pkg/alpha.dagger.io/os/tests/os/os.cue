package os

import (
	"alpha.dagger.io/alpine"
)

// Write a file to an empty dir
EmptyDir: {
	f: #File & {
		path: "/foo.txt"
		write: data: "hello world!"
	}
	f: contents: "hello world!"
}

// Read from a pre-existing file
Read: {
	f: #File & {
		from: alpine.#Image & {
			version: "3.15.0"
		}
		path: "/etc/alpine-release"
	}
	f: contents: "3.15.0\n"
}
