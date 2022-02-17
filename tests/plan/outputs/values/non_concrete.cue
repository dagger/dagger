package main

import "dagger.io/dagger"

dagger.#Plan & {
	outputs: values: test: {
		ok:    "foobar"
		notok: string
	}
}
