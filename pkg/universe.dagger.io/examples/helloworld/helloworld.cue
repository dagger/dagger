// dagger do hello --log-format=plain
//
// 9:06AM INF actions._alpine | computing
// 9:06AM INF actions._alpine | completed    duration=1s
// 9:06AM INF actions.hello | computing
// 9:06AM INF actions.hello | #3 0.073 hello, world!
// 9:06AM INF actions.hello | completed    duration=100ms
package helloworld

import (
	"dagger.io/dagger"
	"dagger.io/dagger/core"
)

dagger.#Plan & {
	actions: {
		_alpine: core.#Pull & {source: "alpine:3"}
		// Hello world
		hello: core.#Exec & {
			input: _alpine.output
			args: ["echo", "hello, world!"]
			always: true
		}
	}
}
