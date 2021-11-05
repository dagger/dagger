package main

import (
	"alpha.dagger.io/dagger"
	"alpha.dagger.io/dagger/llb2"
	"alpha.dagger.io/netlify"
	"alpha.dagger.io/js/yarn"
)

dagger.#Plan

context: {
	// My source code
	import: source: _

	// Docker engine endpoint
	services: docker: _

	// Netlify token
	secrets: netlify: _
}

actions: {
	build: #YarnBuild & {
		// source: context.import.source
		source: llb2.#GitPull & {
			
		}
	}

}


#YarnBuild: {
	localsource: llb2.#Import
}
