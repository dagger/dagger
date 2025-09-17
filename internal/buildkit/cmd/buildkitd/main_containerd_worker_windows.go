package main

import (
	runhcsoptions "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	runtimeoptions "github.com/containerd/containerd/pkg/runtimeoptions/v1"
)

const runtimeRunhcsV1 = "io.containerd.runhcs.v1"

// getRuntimeOptionsType gets empty runtime options by the runtime type name.
func getRuntimeOptionsType(t string) interface{} {
	if t == runtimeRunhcsV1 {
		return &runhcsoptions.Options{}
	}
	return &runtimeoptions.Options{}
}
