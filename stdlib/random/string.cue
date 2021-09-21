// Random generation utilities
package random

import (
	"strconv"

	"alpha.dagger.io/alpine"
	"alpha.dagger.io/dagger/op"
)

// Generate a random string
#String: {
	// Seed of the random string to generate.
	// When using the same `seed`, the same random string will be generated
	// because of caching.
	// FIXME: this is necessary because of https://github.com/dagger/dagger/issues/591
	seed: string & dagger.#Input

	// length of the string
	length: *12 | number & dagger.#Input

	// generated random string
	out: {
		string

		#up: [
			op.#Load & {from: alpine.#Image & {
				package: python3: "=~3.8"
			}},

			op.#WriteFile & {
				dest: "/entrypoint.py"
				content: #"""
					import random
					import string
					import os

					length = int(os.environ['LENGTH'])
					letters = string.ascii_lowercase

					print ( ''.join(random.choice(letters) for i in range(length)) )
					"""#
			},

			op.#Exec & {
				args: ["sh", "-c", #"""
					printf "$(python3 /entrypoint.py)" > /rand
					"""#,
				]
				env: LENGTH: strconv.FormatInt(length, 10)
				env: SEED:   seed

				always: true
			},

			op.#Export & {
				source: "/rand"
			},
		]
	} & dagger.#Output
}
