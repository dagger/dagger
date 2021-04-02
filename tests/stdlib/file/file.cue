package f

import (
	"dagger.io/llb"
	"dagger.io/alpine"
	"dagger.io/file"
)

TestCreate: {
	_content: "hello world"

	write: file.#Create & {
		filename: "/file.txt"
		contents: _content
	}

	test: #up: [
		llb.#Load & {from: alpine.#Image},
		llb.#Exec & {
			args: [
				"sh",
				"-ec",
				"""
					test "$(cat /file.txt)" = "hello world"
					""",
			]
			mount: "/file.txt": {
				from: write
				path: "/file.txt"
			}
		},
	]
}

TestRead: {
	read: file.#Read & {
		filename: "/etc/alpine-release"
		from:     alpine.#Image & {version: "3.10.6"}
	}
	test: #up: [
		llb.#Load & {from: alpine.#Image},
		llb.#Exec & {
			args: [
				"sh",
				"-ec",
				"""
				test "\(read.contents)" = "3.10.6\n"
				""",
			]
		},
	]
}

TestRead2: {
	write: file.#Create & {
		_content: "hello world"
		filename: "/file.txt"
		contents: _content
	}

	read: file.#Read & {
		filename: "/file.txt"
		from:     write
	}

	test: #up: [
		llb.#Load & {from: alpine.#Image},
		llb.#Exec & {
			args: [
				"sh",
				"-ec",
				"""
				test "\(read.contents)" = "hello world"
				""",
			]
		},
	]
}

TestAppend: {
	content1: "hello world"
	content2: "foo bar"

	write: file.#Create & {
		filename: "/file.txt"
		contents: content1
	}
	append: file.#Append & {
		filename: "/file.txt"
		contents: content2
		from:     write
	}

	orig: append.orig

	read: file.#Read & {
		filename: "/file.txt"
		from:     append
	}

	new: read.contents

	test: new & "hello worldfoo bar"
}

TestGlob: {
	list: file.#Glob & {
		glob: "/etc/r*"
		from: alpine.#Image
	}
	test: list.filenames & ["/etc/resolv.conf"]
}
