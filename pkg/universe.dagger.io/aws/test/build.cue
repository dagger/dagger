package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/aws"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		cli: aws.#CLI
		run: docker.#Run & {
			image: cli.output
			cmd: {
				name: "aws"
				args: ["--version"]
			}
		}
	}
}
