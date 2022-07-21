package main

import (
	"context"
	"fmt"

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

			// importAlpineFromImage(ctx)
			importAlpine(ctx, localDirs["."])

			// importTSFromImage(ctx)
			importTS(ctx, localDirs["."])

			input = fmt.Sprintf(`{
				graphql_ts{
					echo(in:"foo") {
						fs
					}
				}
			}`)
			fmt.Printf("input: %+v\n", input)
			output, err = dagger.Do(ctx, input)
			if err != nil {
				return nil, err
			}
			fmt.Printf("output: %+v\n\n", output)

			/*
				input = fmt.Sprintf(`{
					alpine{
						build(
							pkgs: ["curl","jq"],
						)
					}
				}`)
				fmt.Printf("input: %+v\n", input)
				output, err = dagger.Do(ctx, input)
				if err != nil {
					return nil, err
				}
				fmt.Printf("output: %+v\n\n", output)
			*/

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

			if err := engine.Shell(ctx, output.Map("graphql_ts").Map("echo").FS("fs")); err != nil {
				panic(err)
			}

			// fs := output.Map("graphql_ts").Map("echo").FS("fs")
			// fs := localDirs["input"]
			// return &fs, nil
			return nil, nil
		})
	if err != nil {
		panic(err)
	}
}

func importAlpine(ctx context.Context, cwd dagger.FS) {
	input := fmt.Sprintf(`{
		core{
			dockerfile(
				context: %s, 
				dockerfileName: "Dockerfile.alpine",
			)
		}
	}`, cwd)
	output, err := dagger.Do(ctx, input)
	if err != nil {
		panic(err)
	}
	_, err = dagger.Do(ctx, fmt.Sprintf(`mutation{import(name:"alpine",fs:%s){name}}`,
		output.Map("core").FS("dockerfile")))
	if err != nil {
		panic(err)
	}
}

func importAlpineFromImage(ctx context.Context) {
	input := `{
		core{
			image(ref:"localhost:5555/dagger:alpine") {
				fs
			}
		}
	}`
	output, err := dagger.Do(ctx, input)
	if err != nil {
		panic(err)
	}
	_, err = dagger.Do(ctx, fmt.Sprintf(`mutation{import(name:"alpine",fs:%s){name}}`,
		output.Map("core").Map("image").FS("fs")))
	if err != nil {
		panic(err)
	}
}

func importTS(ctx context.Context, cwd dagger.FS) {
	input := fmt.Sprintf(`{
		core{
			dockerfile(
				context: %s, 
				dockerfileName: "Dockerfile.graphql_ts",
			)
		}
	}`, cwd)
	output, err := dagger.Do(ctx, input)
	if err != nil {
		panic(err)
	}
	output, err = dagger.Do(ctx, fmt.Sprintf(`mutation{import(name:"graphql_ts",fs:%s){fs}}`,
		output.Map("core").FS("dockerfile")))
	if err != nil {
		panic(err)
	}
}

func importTSFromImage(ctx context.Context) {
	input := `{
		core{
			image(ref:"localhost:5555/dagger:graphql_ts") {
				fs
			}
		}
	}`
	output, err := dagger.Do(ctx, input)
	if err != nil {
		panic(err)
	}
	_, err = dagger.Do(ctx, fmt.Sprintf(`mutation{import(name:"graphql_ts",fs:%s){name}}`,
		output.Map("core").Map("image").FS("fs")))
	if err != nil {
		panic(err)
	}
}
