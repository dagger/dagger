//go:build !windows
// +build !windows

package main

import (
	runtimeoptions "github.com/containerd/containerd/pkg/runtimeoptions/v1"
	"github.com/containerd/containerd/plugin"
	runcoptions "github.com/containerd/containerd/runtime/v2/runc/options"
)

// getRuntimeOptionsType gets empty runtime options by the runtime type name.
func getRuntimeOptionsType(t string) interface{} {
	if t == plugin.RuntimeRuncV2 {
		return &runcoptions.Options{}
	}
	return &runtimeoptions.Options{}
}
