// A simple main.go for testing the dagger Go API
package main

import (
	"context"
	"fmt"
	"os"

	"dagger.cloud/go/dagger"
)

func main() {
	ctx := context.TODO()
	c, err := dagger.NewClient(ctx, "")
	if err != nil {
		fatal(err)
	}

	configPath := "."
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	if err := c.SetConfig(configPath); err != nil {
		fatal(err)
	}

	// if err := c.ConnectInput("source", os.Getenv("HOME")+"/Documents/github/samalba/hello-go"); err != nil {
	// 	fatal(err)
	// }
	if err := c.Run(ctx, "compute"); err != nil {
		fatal(err)
	}
}

func fatalf(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, msg, args...)
	os.Exit(1)
}

func fatal(msg interface{}) {
	fatalf("%s\n", msg)
}
