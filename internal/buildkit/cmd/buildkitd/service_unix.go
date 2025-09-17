//go:build !windows
// +build !windows

package main

import (
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

// serviceFlags returns an array of flags for configuring buildkitd to run
// as a service. Only relevant on Windows.
func serviceFlags() []cli.Flag {
	return []cli.Flag{}
}

// applyPlatformFlags applies platform-specific flags.
func applyPlatformFlags(context *cli.Context) {
}

// registerUnregisterService is only relevant on Windows.
func registerUnregisterService(_ string) (bool, error) {
	return false, nil
}

// launchService is only relevant on Windows.
func launchService(_ *grpc.Server) error {
	return nil
}
