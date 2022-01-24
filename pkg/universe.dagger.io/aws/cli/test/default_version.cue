package main

import (
	"dagger.io/dagger"
	"universe.dagger.io/aws/cli"
)

dagger.#Plan & {
	actions: version: cli.#Exec & {
		service: "" // blank so as to run `aws --version`
		cmd: name:        ""
		options: version: true
		unmarshal: false
		result:    =~"^aws-cli/2.4.12"
	}
}
