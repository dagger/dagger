package main

var (
	// Package is filled at linking time
	Package = "github.com/moby/buildkit/frontend/dockerfile/cmd/dockerfile-frontend"

	// Version holds the complete version number. Filled in at linking time.
	Version = "0.0.0+unknown"

	// Revision is filled with the VCS (e.g. git) revision being used to build
	// the program at linking time.
	Revision = ""
)
