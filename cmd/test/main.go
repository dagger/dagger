package main

import (
	"context"
	"fmt"

	bkclient "github.com/moby/buildkit/client"

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
	 */
	outputDir := "./output"
	startOpts = &engine.StartOpts{
		Export: &bkclient.ExportEntry{
			Type:      bkclient.ExporterLocal,
			OutputDir: outputDir,
		},
		LocalDirs: map[string]string{
			"input": "./input",
		},
	}

	err := engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS) (*dagger.FS, error) {
			var input string
			var output *dagger.Map
			var err error

			/*
				_, err = dagger.Do(ctx, `mutation{import(ref:"helloworld_ts"){name}}`)
				if err != nil {
					return err
				}
			*/

			_, err = dagger.Do(ctx, `mutation{import(ref:"alpine"){name}}`)
			if err != nil {
				return nil, err
			}
			_, err = dagger.Do(ctx, `mutation{import(ref:"graphql_ts"){name}}`)
			if err != nil {
				return nil, err
			}

			/*
				output, err = dagger.Do(ctx, tools.IntrospectionQuery)
				if err != nil {
					return err
				}
				fmt.Printf("schema: %s\n", output)
			*/

			input = `{
			graphql_ts{
				echo(in:"hey"){
					fs
				}
			}
		}`
			fmt.Printf("input: %+v\n", input)
			output, err = dagger.Do(ctx, input)
			if err != nil {
				return nil, err
			}
			fmt.Printf("output: %+v\n\n", output)

			/*
				input = fmt.Sprintf(`mutation{evaluate(fs:%s)}`, output.Map("graphql_ts").Map("echo").FS("fs"))
				fmt.Printf("input: %+v\n", input)
				output, err = dagger.Do(ctx, input)
				if err != nil {
					return nil, err
				}
				fmt.Printf("output: %+v\n\n", output)

					if err := engine.Shell(ctx, output.FS("evaluate")); err != nil {
						panic(err)
					}
			*/

			// fs := output.Map("graphql_ts").Map("echo").FS("fs")
			fs := localDirs["input"]
			return &fs, nil
		})
	if err != nil {
		panic(err)
	}
}
