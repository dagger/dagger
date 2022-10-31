// Build demonstrates how dagger can by used together with goyek.
package main

import (
	"os"

	"github.com/goyek/goyek/v2"
	"github.com/goyek/goyek/v2/middleware"
)

func main() {
	goyek.Use(middleware.ReportStatus)
	goyek.Main(os.Args[1:])
}
