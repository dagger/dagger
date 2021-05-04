package main

import (
	"dagger.io/docker"
	"dagger.io/os"
)

let ctr = docker.#Container & {
	command: "echo 'hello world!' > /etc/motd"
}

motd: (os.#File & {
	from: ctr
	path: "/etc/motd"
	read: format: "string"
}).read.data

etc: (os.#Dir & {
	from: ctr
	path: "/etc"
}).read.tree
