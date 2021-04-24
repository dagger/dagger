package testing

import "dagger.io/dagger/op"

TestExportConcurrency1: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol1 > /tmp/out
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

TestExportConcurrency2: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol2 > /tmp/out
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

TestExportConcurrency3: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol3 > /tmp/out
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

TestExportConcurrency4: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol4 > /tmp/out
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

TestExportConcurrency5: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol5 > /tmp/out
				"""]
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

TestExportConcurrency6: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol6 > /tmp/out
				"""]
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

TestExportConcurrency7: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol7 > /tmp/out
				"""]
			always: true
		},
		op.#Export & {
			source: "/tmp/out"
			format: "string"
		},
	]
}

TestExportConcurrency8: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol8 > /tmp/out
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

TestExportConcurrency9: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol9 > /tmp/out
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

TestExportConcurrency10: {
	string

	#up: [
		op.#FetchContainer & {
			ref: "alpine"
		},
		op.#Exec & {
			args: ["sh", "-c", """
					echo -n lol10 > /tmp/out
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
