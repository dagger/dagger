package main

import (
	"dagger.io/docker"
	"dagger.io/io"
)

let ctr = docker.#Container & {
	command: "echo 'hello world!' > /etc/motd"
}

motd: (io.#File & {
	from: ctr
	path: "/etc/motd"
	read: format: "string"
}).read.data

etc: (io.#Dir & {
	from: ctr
	path: "/etc"
}).read.tree
