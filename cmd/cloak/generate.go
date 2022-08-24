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
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use: "generate",
	Run: Generate,
}

func Generate(cmd *cobra.Command, args []string) {
	localDirs := map[string]string{
		projectContextLocalName: projectContext,
	}
	startOpts := &engine.Config{
		LocalDirs: localDirs,
	}

	err := engine.Start(context.Background(), startOpts, func(ctx context.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		localDirs, err := loadLocalDirs(ctx, cl, localDirs)
		if err != nil {
			return err
		}

		project, err := loadProject(ctx, cl, localDirs[projectContextLocalName])
		if err != nil {
			return err
		}

		coreExt, err := loadCore(ctx, cl)
		if err != nil {
			return err
		}

		switch sdkType {
		case "go":
			if err := generateGoImplStub(project, coreExt); err != nil {
				return err
			}
		case "":
		default:
			return fmt.Errorf("unknown sdk type %s", sdkType)
		}

		for _, dep := range append(project.Dependencies, coreExt) {
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

func loadProject(ctx context.Context, cl graphql.Client, contextFS dagger.FSID) (*core.Extension, error) {
	res := struct {
		Core struct {
			Filesystem struct {
				LoadExtension core.Extension
			}
		}
	}{}
	resp := &graphql.Response{Data: &res}

	err := cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query LoadExtension($fs: FSID!, $configPath: String!) {
				core {
					filesystem(id: $fs) {
						loadExtension(configPath: $configPath) {
							name
							schema
							operations
							dependencies {
								name
								schema
								operations
							}
						}
					}
				}
			}`,
			Variables: map[string]any{
				"fs":         contextFS,
				"configPath": projectFile,
			},
		},
		resp,
	)
	if err != nil {
		return nil, err
	}

	return &res.Core.Filesystem.LoadExtension, nil
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
