package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"go.dagger.io/dagger/codegen/generator"
	"go.dagger.io/dagger/codegen/introspection"
	"go.dagger.io/dagger/engine"
)

var clientGenCmd = &cobra.Command{
	Use: "client-gen",
	Run: ClientGen,
}

func init() {
	clientGenCmd.Flags().StringP("output", "o", "", "output file")
	clientGenCmd.Flags().String("package", "", "package name")
}

func ClientGen(cmd *cobra.Command, args []string) {
	startOpts := &engine.Config{
		Workdir:     workdir,
		ConfigPath:  configPath,
		SkipInstall: true,
	}

	pkg, err := getPackage(cmd)
	if err != nil {
		panic(err)
	}

	var generated []byte
	if err := engine.Start(context.Background(), startOpts, func(ctx engine.Context) error {
		var response introspection.Response
		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: introspection.Query,
			},
			&graphql.Response{Data: &response},
		)
		if err != nil {
			return fmt.Errorf("error querying the API: %w", err)
		}

		generated, err = generator.Generate(ctx, response.Schema, generator.Config{
			Package: pkg,
		})
		return err
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	output, err := cmd.Flags().GetString("output")
	if err != nil {
		panic(err)
	}

	if output == "" || output == "-" {
		fmt.Fprint(os.Stdout, string(generated))
	} else {
		if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(output, generated, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		gitAttributes := fmt.Sprintf("/%s linguist-generated=true", filepath.Base(output))
		if err := os.WriteFile(path.Join(filepath.Dir(output), ".gitattributes"), []byte(gitAttributes), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

func getPackage(cmd *cobra.Command) (string, error) {
	pkg, err := cmd.Flags().GetString("package")
	if err != nil {
		return "", err
	}

	if pkg != "" {
		return pkg, nil
	}

	// Come up with a default package name
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return "", err
	}
	if output == "" || output == "-" {
		return "main", nil
	}

	directory, err := filepath.Abs(filepath.Dir(output))
	if err != nil {
		return "", err
	}
	return filepath.Base(directory), nil
}
