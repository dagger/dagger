package auth

import (
	"dagger.io/dagger/core"
	"universe.dagger.io/docker"
	azauth "universe.dagger.io/alpha/azure/auth"

)

#Image: docker.#Copy & {
	_img: azauth.#Image
	_src: core.#Source & {
		path: "."
	}
	input:    _img.output
	contents: _src.output
	source:   "akscreds.sh"
	dest:     "/usr/local/bin/akscreds"
}
