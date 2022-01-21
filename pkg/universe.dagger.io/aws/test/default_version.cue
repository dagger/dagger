package test

import (
	"dagger.io/dagger"
	"universe.dagger.io/aws"
)

dagger.#Plan & {
	actions: version: aws.#CLI & {
		service: ""
		options: version: true
		cmd: name:        ""
		export: files: "/output.txt": contents: =~"^aws-cli/2.4.12"
	}
}
