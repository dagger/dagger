package main

import (
	_ "embed"

	"context"
	"fmt"
	"os"
	"path/filepath"

	coreschema "github.com/dagger/cloak/api/schema"
	"github.com/dagger/cloak/cmd/cloak/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use: "generate",
	Run: Generate,
}

func Generate(cmd *cobra.Command, args []string) {
	cfg, err := config.ParseFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	switch sdkType {
	case "go":
		if err := generateGoImplStub(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "":
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown sdk type %s\n", sdkType)
		os.Exit(1)
	}

	startOpts := &engine.Config{
		LocalDirs: cfg.LocalDirs(),
	}
	err = engine.Start(context.Background(), startOpts, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		localDirs, err := loadLocalDirs(ctx, cl, cfg.LocalDirs())
		if err != nil {
			return err
		}

		if err := cfg.LoadExtensions(ctx, localDirs); err != nil {
			return err
		}
		for name, act := range cfg.Actions {
			subdir := filepath.Join(generateOutputDir, "gen", name)
			if err := os.MkdirAll(subdir, 0755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(subdir, ".gitattributes"), []byte("** linguist-generated=true"), 0644); err != nil {
				return err
			}
			schemaPath := filepath.Join(subdir, "schema.graphql")

			// TODO: ugly hack to make each schema/operation work independently when referencing core types
			fullSchema := act.GetSchema()
			if name != "core" {
				fullSchema = coreschema.Schema + "\n\n" + fullSchema
			}

			if err := os.WriteFile(schemaPath, []byte(fullSchema), 0644); err != nil {
				return err
			}
			operationsPath := filepath.Join(subdir, "operations.graphql")
			if err := os.WriteFile(operationsPath, []byte(act.GetOperations()), 0644); err != nil {
				return err
			}

			switch sdkType {
			case "go":
				if err := generateGoClientStubs(subdir); err != nil {
					return err
				}
			case "":
			default:
				fmt.Fprintf(os.Stderr, "Error: unknown sdk type %s\n", sdkType)
				os.Exit(1)
			}
		}
		return nil
	},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
