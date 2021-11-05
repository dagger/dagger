package main

import (
	"alpha.dagger.io/dagger/llb2"
)

#ContextImport: {
	llb2.#Import
	path: string
}
#ContextSecret: {
	llb2.#Secret
	envvar: string
}

mysource: #ContextImport

mytoken: #ContextSecret

actions: {
	deploy: {
		contents: llb2.#FS & mysource
		token: llb2.#Mountable & mytoken
	}
}
