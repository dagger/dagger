package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func main() {
	/*
		if err := engine.RunGraphiQL(context.Background(), 8080); err != nil {
			panic(err)
		}
	*/

	var startOpts *engine.StartOpts

	/*
		outputDir := "./output"
	*/

	startOpts = &engine.StartOpts{
		/*
			Export: &bkclient.ExportEntry{
				Type:      bkclient.ExporterLocal,
				OutputDir: outputDir,
			},
		*/
		LocalDirs: map[string]string{
			".": ".",
		},
	}

	err := engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS) (*dagger.FS, error) {
			var input string
			var output *dagger.Map
			var err error

			/*
				output, err = dagger.Do(ctx, tools.IntrospectionQuery)
				if err != nil {
					return err
				}
				fmt.Printf("schema: %s\n", output)
			*/

			importLocal(ctx, localDirs["."], "alpine", "Dockerfile.alpine")
			// importImage(ctx, "alpine", "localhost:5555/dagger:alpine")

			importLocal(ctx, localDirs["."], "graphql_ts", "Dockerfile.graphql_ts")
			// importImage(ctx, "graphql_ts", "localhost:5555/dagger:graphql_ts")

			input = fmt.Sprintf(`{
					alpine{
						build(
							pkgs: ["curl","jq"],
						)
					}
				}`)
			fmt.Printf("input: %+v\n", input)
			alpine, err := dagger.Do(ctx, input)
			if err != nil {
				return nil, err
			}
			fmt.Printf("output: %+v\n\n", alpine)

			/*
			 */
			input = fmt.Sprintf(`{
					graphql_ts{
						echo(in:"foo",fs:%q) {
							fs
							out
						}
					}
				}`, alpine.Map("alpine").FS("build"))
			fmt.Printf("input: %+v\n", input)
			output, err = dagger.Do(ctx, input)
			if err != nil {
				return nil, err
			}
			fmt.Printf("output: %+v\n\n", output)

			fmt.Printf("a string: %s\n", output.Map("graphql_ts").Map("echo").String("out"))

			// input = fmt.Sprintf(`mutation{evaluate(fs:%s)}`, output.Map("alpine").FS("build"))
			/*
				input = fmt.Sprintf(`mutation{evaluate(fs:%s)}`, output.Map("graphql_ts").Map("echo").FS("fs"))
				fmt.Printf("input: %+v\n", input)
				output, err = dagger.Do(ctx, input)
				if err != nil {
					return nil, err
				}
				fmt.Printf("output: %+v\n\n", output)
			*/

			// if err := engine.Shell(ctx, output.Map("graphql_ts").Map("echo").FS("fs")); err != nil {
			if err := engine.Shell(ctx, alpine.Map("alpine").FS("build")); err != nil {
				return nil, err
			}

			// fs := output.Map("graphql_ts").Map("echo").FS("fs")
			// fs := localDirs["input"]
			// return &fs, nil
			return nil, nil
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
