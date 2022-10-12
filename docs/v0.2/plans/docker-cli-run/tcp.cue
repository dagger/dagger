package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/docker/cli"
)

dagger.#Plan & {
	// Directory with certificates. Needs the following files:
	//  - ca.pem   --> (Certificate authority that signed the registry certificate)
	//  - cert.pem --> (Client certificate)
	//  - key.pem  --> (Client private key)
	client: filesystem: "./certs": read: contents: dagger.#FS

	actions: run: cli.#Run & {
		host:  "tcp://93.184.216.34:2376"
		certs: client.filesystem."./certs".read.contents
		command: name: "info"
	}
}
