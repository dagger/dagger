package alpine

import (
	"dagger.io/dagger/op"
)

let defaultVersion = "3.13.2@sha256:a75afd8b57e7f34e4dad8d65e2c7ba2e1975c795ce1ee22fa34f8cf46f96a3be"

#Image: {
	package: [string]: true | false | string
	version: string | *defaultVersion

	#up: [
		op.#FetchContainer & {
			ref: "index.docker.io/alpine:\(version)"
		},
		for pkg, info in package {
			if (info & true) != _|_ {
				op.#Exec & {
					args: ["apk", "add", "-U", "--no-cache", pkg]
				}
			}
			if (info & string) != _|_ {
				op.#Exec & {
					args: ["apk", "add", "-U", "--no-cache", "\(pkg)\(info)"]
				}
			}
		},
	]
}
