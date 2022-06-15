package main

import (
	"fmt"
	"os"

	"github.com/dagger/cloak/dagger"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <pkg> <action> [<payload>]\n", os.Args[0])
		os.Exit(1)
	}
	pkg, action, payload := os.Args[1], os.Args[2], ""
	if len(os.Args) > 3 {
		payload = os.Args[3]
	}
	var output *dagger.Output
	err := dagger.Client(func(ctx *dagger.Context) error {
		var err error
		output, err = dagger.Do(ctx, pkg, action, payload)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("%s\n", string(output.Raw()))
}
