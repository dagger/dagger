// Package main is a stub entrypoint for the Dang SDK container image.
//
// The Dang SDK uses a native runtime (DangRuntime) that executes directly
// in the engine process, so this entrypoint is not actually invoked at
// runtime. It exists only to satisfy the SDK container image packaging
// used by the engine build system.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "dang-entrypoint: the Dang SDK uses a native runtime and does not use this entrypoint")
	os.Exit(1)
}
