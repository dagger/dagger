//go:generate go run github.com/Khan/genqlient ./gen/todoapp/genqlient.yaml
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Khan/genqlient/graphql"

	"github.com/dagger/cloak/cmd/web/config"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func main() {
	cfg, err := config.ParseFile("./dagger.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	localDirs := cfg.LocalDirs()
	// TODO: read this from cli flags
	localDirs["src"] = "../../examples/todoapp/app"

	startOpts := &engine.StartOpts{
		LocalDirs: localDirs,
	}

	err = engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error) {
			if err := cfg.Import(ctx, localDirs); err != nil {
				return nil, err
			}

			inBytes, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, err
			}

			vars := map[string]any{}
			for name, fs := range localDirs {
				// TODO: need better naming convention
				vars["local_"+name] = fs
			}

			cl, err := dagger.Client(ctx)
			if err != nil {
				return nil, err
			}
			res := make(map[string]interface{})
			resp := &graphql.Response{Data: &res}
			err = cl.MakeRequest(ctx,
				&graphql.Request{
					Query:     string(inBytes),
					Variables: vars,
				},
				resp,
			)
			if err != nil {
				return nil, err
			}
			if len(resp.Errors) > 0 {
				return nil, resp.Errors
			}
			return nil, nil
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
