package main

import (
	"os"

	"golang.org/x/mod/semver"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(1)
	}

	version := os.Args[1]
	if !semver.IsValid(version) {
		os.Exit(1)
	}

	os.Exit(0)
}
