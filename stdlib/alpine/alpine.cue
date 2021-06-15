// Base package for Alpine Linux
package alpine

import (
	"dagger.io/dagger/op"
)

// Default Alpine version
let defaultVersion = "3.13.5@sha256:69e70a79f2d41ab5d637de98c1e0b055206ba40a8145e7bddb55ccc04e13cf8f"

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
