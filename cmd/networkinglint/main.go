// Command networkinglint checks that engine network connections select an
// owner before they send traffic.
package main

import (
	"github.com/dagger/dagger/internal/networkinglint"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(networkinglint.Analyzer)
}
