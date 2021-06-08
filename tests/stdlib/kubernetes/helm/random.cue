package main

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
			op.#Load & {from: alpine.#Image & {
				package: python3: "=~3.8"
			}},

			op.#WriteFile & {
				dest: "/entrypoint.py"
				content: #"""
					import random
					import string
					import os

					size = int(os.environ['SIZE'])
					letters = string.ascii_lowercase

					print ( ''.join(random.choice(letters) for i in range(size)) )
					"""#
			},

			op.#Exec & {
				always: true
				args: ["sh", "-c", #"""
					printf "$(python3 /entrypoint.py)" > /rand
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
