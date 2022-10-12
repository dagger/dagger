package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/sdk/go/dagger"
)

// nolint:deadcode,unused,varcheck
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

	if err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		generatedCodeFS, err := projectGeneratedCode(ctx, cl, ctx.Workdir, ctx.ConfigPath)
		if err != nil {
			return err
		}

		if err := export(ctx, cl, generatedCodeFS); err != nil {
			return err
		}

		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func projectGeneratedCode(ctx context.Context, cl graphql.Client, projectDir core.DirectoryID, configPath string) (core.DirectoryID, error) {
	data := struct {
		Directory struct {
			LoadProject struct {
				GeneratedCode struct {
					ID core.DirectoryID
				}
			}
		}
	}{}
	resp := &graphql.Response{Data: &data}

	err := cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query GeneratedCode($fs: FSID!, $configPath: String!) {
				core {
					filesystem(id: $fs) {
						loadProject(configPath: $configPath) {
							generatedCode {
								id
							}
						}
					}
				}
			}`,
			Variables: map[string]any{
				"fs":         projectDir,
				"configPath": configPath,
			},
		},
		resp,
	)
	if err != nil {
		return "", err
	}
	return data.Directory.LoadProject.GeneratedCode.ID, nil
}

func export(ctx context.Context, cl graphql.Client, dir core.DirectoryID) error {
	data := struct {
		Host struct {
			Workdir struct {
				Write bool
			}
		}
	}{}
	resp := &graphql.Response{Data: &data}

	err := cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query Export($dir: DirectoryID!) {
				host {
					workdir {
						write(contents: $dir)
					}
				}
			}`,
			Variables: map[string]any{
				"dir": dir,
			},
		},
		resp,
	)
	if err != nil {
		return err
	}
	return nil
}
