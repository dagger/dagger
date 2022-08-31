package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/core"
	"github.com/dagger/cloak/engine"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use: "generate",
	Run: Generate,
}

func Generate(cmd *cobra.Command, args []string) {
	startOpts := &engine.Config{
		Workdir:     workdir,
		ConfigPath:  configPath,
		SkipInstall: true,
	}

	err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		coreExt, err := loadCore(ctx, ctx.Client)
		if err != nil {
			return err
		}

		switch sdkType {
		case "go":
			if err := generateGoImplStub(ctx.Extension, coreExt); err != nil {
				return err
			}
		case "":
		default:
			return fmt.Errorf("unknown sdk type %s", sdkType)
		}

		for _, dep := range append(ctx.Extension.Dependencies, coreExt) {
			subdir := filepath.Join(generateOutputDir, "gen", dep.Name)
			if err := os.MkdirAll(subdir, 0755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(subdir, ".gitattributes"), []byte("** linguist-generated=true"), 0600); err != nil {
				return err
			}
			schemaPath := filepath.Join(subdir, "schema.graphql")

			// TODO:(sipsma) ugly hack to make each schema/operation work independently when referencing core types.
			fullSchema := dep.Schema
			if dep.Name != "core" {
				fullSchema = coreExt.Schema + "\n\n" + fullSchema
			}
			if err := os.WriteFile(schemaPath, []byte(fullSchema), 0600); err != nil {
				return err
			}
			operationsPath := filepath.Join(subdir, "operations.graphql")
			if err := os.WriteFile(operationsPath, []byte(dep.Operations), 0600); err != nil {
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

func loadCore(ctx context.Context, cl graphql.Client) (*core.Extension, error) {
	data := struct {
		Core struct {
			Extension core.Extension
		}
	}{}
	resp := &graphql.Response{Data: &data}

	err := cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query {
				core {
					extension(name: "core") {
						name
						schema
						operations
					}
				}
			}`,
		},
		resp,
	)
	if err != nil {
		return nil, err
	}
	return &data.Core.Extension, nil
}
