package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
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

	if err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		cl, err := dagger.Client(ctx)
		if err != nil {
			return err
		}

		generatedCodeFS, err := projectGeneratedCode(ctx, cl, ctx.Workdir, ctx.ConfigPath)
		if err != nil {
			return err
		}

		if err := export(ctx, cl, generatedCodeFS.ID); err != nil {
			return err
		}

		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func projectGeneratedCode(ctx context.Context, cl graphql.Client, projectFS dagger.FSID, configPath string) (*dagger.Filesystem, error) {
	data := struct {
		Core struct {
			Filesystem struct {
				LoadProject struct {
					GeneratedCode dagger.Filesystem
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
				"fs":         projectFS,
				"configPath": configPath,
			},
		},
		resp,
	)
	if err != nil {
		return nil, err
	}
	return &data.Core.Filesystem.LoadProject.GeneratedCode, nil
}

func export(ctx context.Context, cl graphql.Client, fs dagger.FSID) error {
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
			query Export($fs: FSID!) {
				host {
					workdir {
						write(contents: $fs)
					}
				}
			}`,
			Variables: map[string]any{
				"fs": fs,
			},
		},
		resp,
	)
	if err != nil {
		return err
	}
	return nil
}
