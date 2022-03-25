package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	client: filesystem: {
		"/home/user/.ssh/id_rsa": read: contents:      dagger.#Secret
		"/home/user/.ssh/known_hosts": read: contents: dagger.#Secret
	}

	actions: run: cli.#Run & {
		host: "ssh://root@93.184.216.34"
		ssh: {
			key:        client.filesystem."/home/user/.ssh/id_rsa".read.contents
			knownHosts: client.filesystem."/home/user/.ssh/known_hosts".read.contents
		}
		command: name: "info"
	}
}
