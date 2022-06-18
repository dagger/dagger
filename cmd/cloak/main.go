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
	err := dagger.Client(func(ctx *dagger.Context) error {
		payload, err := dagger.Marshal(ctx, payload)
		if err != nil {
			panic(err)
		}

		output, err := dagger.Do(ctx, pkg, action, payload)
		if err != nil {
			panic(err)
		}

		var stringOutput string
		if err := dagger.Unmarshal(ctx, output, &stringOutput); err != nil {
			return err
		}
		fmt.Printf("%s\n", stringOutput)

		// NOTE: interesting use for dynamic-ish data here to get an FS from any output:
		type fsOutput struct {
			FS dagger.FS `json:"fs,omitempty"`
		}
		var fs fsOutput
		if err := dagger.Unmarshal(ctx, output, &fs); err == nil {
			if err := dagger.Shell(ctx, fs.FS); err != nil {
				panic(err)
			}
		}

		return nil
	})
	if err != nil {
		panic(err)
	}

}
