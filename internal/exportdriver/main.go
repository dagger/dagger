// export-driver is a GOPACKAGESDRIVER for golangci-lint that serves export
// data from prebuilt per-package archives instead of compiling every
// dependency with `go list -export`.
//
// golangci-lint type-checks the packages it lints from source and loads every
// other package from export data. With the driver, a package's export data is
// its prebuilt archive when the archive's sources match the package's;
// otherwise the driver compiles it, or asks the go command as golangci-lint
// would. When the archives or the request may not match what the go command
// would build, the driver declines and golangci-lint uses go list itself.
//
// Run as `export-driver --write-manifest=DIR [build flags] -- patterns` it
// writes such a directory from the go command's own export data, for tests.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "--write-manifest=") {
		err = writeManifestDir(ctx, strings.TrimPrefix(os.Args[1], "--write-manifest="), os.Args[2:])
	} else {
		err = runDriver(ctx, os.Args[1:], os.Stdin, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "export-driver:", err)
		os.Exit(1)
	}
}
