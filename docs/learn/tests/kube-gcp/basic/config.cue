package main

import (
	"alpha.dagger.io/gcp"
	"alpha.dagger.io/gcp/gke"
)

// Value created for generic reference of `kubeconfig` in `todoapp.cue`
kubeconfig: gkeConfig.kubeconfig

// gcpConfig used for Google connection
gcpConfig: gcp.#Config

// gkeConfig used for deployment  
gkeConfig: gke.#KubeConfig & {
	// config field references `gkeConfig` value to set in once
	config: gcpConfig
}
