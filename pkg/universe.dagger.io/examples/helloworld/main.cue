// dagger do hello --log-format=plain
//
// 9:06AM INF actions._alpine | computing
// 9:06AM INF actions._alpine | completed    duration=1s
// 9:06AM INF actions.hello | computing
// 9:06AM INF actions.hello | #3 0.073 hello, world!
// 9:06AM INF actions.hello | completed    duration=100ms
package main

import (
	"dagger.io/dagger"
)

dagger.#Plan & {
	actions: {
		_alpine: dagger.#Pull & {source: "alpine:3"}
		// Hello world
		hello: dagger.#Exec & {
			input: _alpine.output
			args: ["echo", "hello, world!"]
			always: true
		}
	}
}
