package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/cmd/web/config"
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
		LocalDirs: make(map[string]string),
	}
	for _, action := range cfg.Actions {
		if action.Local != "" {
			startOpts.LocalDirs[action.Local] = action.Local
		}
	}

	err = engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS) (*dagger.FS, error) {
			for name, action := range cfg.Actions {
				switch {
				case action.Local != "":
					importLocal(ctx, localDirs[action.Local], name, action.Dockerfile)
				case action.Image != "":
					importImage(ctx, name, action.Image)
				}
			}
			return nil, engine.ListenAndServe(ctx, 8080)
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func importLocal(ctx context.Context, cwd dagger.FS, name string, dockerfile string) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		panic(err)
	}
	data := struct {
		Core struct {
			Dockerfile dagger.FS
		}
	}{}
	resp := &graphql.Response{Data: &data}
	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query Dockerfile($context: FS!, $dockerfile: String!) {
				core{
					dockerfile(
						context: $context,
						dockerfileName: $dockerfile,
					)
				}
			}`,
			Variables: map[string]any{
				"context":    cwd,
				"dockerfile": dockerfile,
			},
		},
		resp,
	)
	if err != nil {
		panic(err)
	}

	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			mutation Import($name: String!, $fs: FS!) {
				import(name: $name, fs: $fs) {
						name
				}
			}`,
			Variables: map[string]any{
				"name": name,
				"fs":   data.Core.Dockerfile,
			},
		},
		&graphql.Response{},
	)
	if err != nil {
		panic(err)
	}
}

func importImage(ctx context.Context, name string, ref string) {
	cl, err := dagger.Client(ctx)
	if err != nil {
		panic(err)
	}
	data := struct {
		Core struct {
			Image struct {
				FS dagger.FS
			}
		}
	}{}
	resp := &graphql.Response{Data: &data}
	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			query Image($ref: String!) {
				core{
					image(ref: $ref) {
						fs
					}
				}
			}`,
			Variables: map[string]any{
				"ref": ref,
			},
		},
		resp,
	)
	if err != nil {
		panic(err)
	}

	err = cl.MakeRequest(ctx,
		&graphql.Request{
			Query: `
			mutation Import($name: String!, $fs: FS!) {
				import(name: $name, fs: $fs) {
						name
				}
			}`,
			Variables: map[string]any{
				"name": name,
				"fs":   data.Core.Image.FS,
			},
		},
		&graphql.Response{},
	)
	if err != nil {
		panic(err)
	}

}
