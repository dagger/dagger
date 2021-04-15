package testing

import "dagger.io/dagger/op"

test1: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol1 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test2: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol2 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test3: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol3 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test4: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol4 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test5: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol5 > /tmp/out
				"""]
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test6: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol6 > /tmp/out
				"""]
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test7: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol7 > /tmp/out
				"""]
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test8: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol8 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test9: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol9 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

test10: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo lol10 > /tmp/out
				"""]
			dir:    "/"
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}
