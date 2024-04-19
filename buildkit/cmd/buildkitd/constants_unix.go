//go:build !windows
// +build !windows

package main

const (
	defaultContainerdAddress = "/run/containerd/containerd.sock"
)
