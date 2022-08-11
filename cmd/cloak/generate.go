package main

import (
	_ "embed"

	"context"
	"fmt"
	"os"
	"path/filepath"

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

		switch sdkType {
		case "go":
			if err := generateGoImplStub(cfg.Extensions["core"].GetSchema()); err != nil {
				return err
			}
		case "":
		default:
			return fmt.Errorf("unknown sdk type %s", sdkType)
		}

		for name, ext := range cfg.Extensions {
			subdir := filepath.Join(generateOutputDir, "gen", name)
			if err := os.MkdirAll(subdir, 0755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(subdir, ".gitattributes"), []byte("** linguist-generated=true"), 0600); err != nil {
				return err
			}
			schemaPath := filepath.Join(subdir, "schema.graphql")

			fullSchema := ext.GetSchema()
			if name != "core" {
				// TODO:(sipsma) ugly hack to make each schema/operation work independently when referencing core types.
				fullSchema = cfg.Extensions["core"].GetSchema() + "\n\n" + fullSchema
			}

			if err := os.WriteFile(schemaPath, []byte(fullSchema), 0600); err != nil {
				return err
			}
			operationsPath := filepath.Join(subdir, "operations.graphql")
			if err := os.WriteFile(operationsPath, []byte(ext.GetOperations()), 0600); err != nil {
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
