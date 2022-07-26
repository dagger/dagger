package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/cloak/cmd/dev/config"
	"github.com/dagger/cloak/engine"
)

func main() {
	f := "./dagger.yaml"
	if len(os.Args) > 1 {
		f = os.Args[1]
	}
	cfg, err := config.ParseFile(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	startOpts := &engine.StartOpts{
		LocalDirs: cfg.LocalDirs(),
		Secrets:   make(map[string]string),
		DevServer: 8080,
	}

	err = engine.Start(context.Background(), startOpts, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
