package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/aws/cli"
)

dagger.#Plan & {
	actions: version: cli.#Run & {
		service: "" // blank so as to run `aws --version`
		command: name:    ""
		options: version: true
		unmarshal: false
		export: files: "/output.txt": contents: =~"^aws-cli/2.4.12"
	}
}
