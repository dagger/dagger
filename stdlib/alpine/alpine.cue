// Base package for Alpine Linux
package alpine

import (
	"alpha.dagger.io/dagger/op"
)

// Default Alpine version
let defaultVersion = "3.15.0@sha256:c74f1b1166784193ea6c8f9440263b9be6cae07dfe35e32a5df7a31358ac2060"

// Base image for Alpine Linux
#Image: {
	// List of packages to install
	package: [string]: true | false | string
	// Alpine version to install
	version: string | *defaultVersion

	// Use of os package not possible : alpine is a low level component
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
