package gcr

import (
	"strconv"

	"dagger.io/alpine"
	"dagger.io/dagger/op"
)

#Random: {
	size: *12 | number

	out: {
		string

		#up: [
			op.#Load & {from: alpine.#Image},

			op.#Exec & {
				always: true
				args: ["sh", "-c", #"""
					tr -cd '[:alpha:]' < /dev/urandom | fold -w "$SIZE" | head -n 1 | tr '[A-Z]' '[a-z]' | tr -d '\n' > /rand
					"""#,
				]
				env: SIZE: strconv.FormatInt(size, 10)
			},

			op.#Export & {
				source: "/rand"
			},
		]
	}
}
