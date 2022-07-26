package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/cloak/cmd/dev/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/spf13/cobra"
)

// TODO: put in own file
func Generate(cmd *cobra.Command, args []string) {
	cfg, err := config.ParseFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	startOpts := &engine.StartOpts{
		LocalDirs: cfg.LocalDirs(),
	}
	err = engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error) {
			if err := cfg.Import(ctx, localDirs); err != nil {
				return nil, err
			}
			for name, act := range cfg.Actions {
				subdir := filepath.Join(generateOutpuDir, name)
				if err := os.MkdirAll(subdir, 0755); err != nil {
					return nil, err
				}
				schemaPath := filepath.Join(subdir, "schema.graphql")
				if err := os.WriteFile(schemaPath, []byte(act.GetSchema()), 0644); err != nil {
					return nil, err
				}
				operationsPath := filepath.Join(subdir, "operations.graphql")
				if err := os.WriteFile(operationsPath, []byte(act.GetOperations()), 0644); err != nil {
					return nil, err
				}
			}
			return nil, nil
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
