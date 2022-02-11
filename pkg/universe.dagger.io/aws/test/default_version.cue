package aws

import (
	"dagger.io/dagger"
	"universe.dagger.io/aws"
	"universe.dagger.io/docker"
)

dagger.#Plan & {
	actions: {
		build: aws.#Build

		getVersion: docker.#Run & {
			input: build.output
			command: {
				name: "sh"
				flags: "-c": true
				args: ["aws --version > /output.txt"]
			}
			export: files: "/output.txt": contents: =~"^aws-cli/2.4.12"
		}
	}
}
