package main

import (
	"alpha.dagger.io/dagger"
)

// set with `dagger input text kubeconfig -f "$HOME"/.kube/config -e kube`
kubeconfig: string & dagger.#Input
