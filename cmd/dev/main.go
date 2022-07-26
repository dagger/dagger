package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/cloak/cmd/dev/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
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
	}

	err = engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error) {
			if err := cfg.Import(ctx, localDirs); err != nil {
				return nil, err
			}
			return nil, engine.ListenAndServe(ctx, 8080)
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
