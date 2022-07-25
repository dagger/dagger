//go:generate go run github.com/Khan/genqlient ./gen/todoapp/genqlient.yaml
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/cmd/cloak/gen/core"
	"golang.org/x/sync/errgroup"

	// "github.com/dagger/cloak/cmd/cloak/gen/todoapp"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func main() {
	startOpts := &engine.StartOpts{
		// TODO: read these from cli flags
		LocalDirs: map[string]string{
			".":   ".",
			"src": "./examples/todoapp/app",
		},
	}

	err := engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error) {
			importAll(ctx, localDirs)
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

func importAll(ctx context.Context, localDirs map[string]dagger.FS) {
	var eg errgroup.Group
	eg.Go(func() error {
		importLocal(ctx, localDirs["."], "alpine", "Dockerfile.alpine")
		return nil
	})
	eg.Go(func() error {
		importLocal(ctx, localDirs["."], "netlify", "Dockerfile.netlify")
		return nil
	})
	eg.Go(func() error {
		importLocal(ctx, localDirs["."], "yarn", "Dockerfile.yarn")
		return nil
	})
	eg.Go(func() error {
		importLocal(ctx, localDirs["."], "todoapp", "Dockerfile.todoapp")
		return nil
	})
	err := eg.Wait()
	if err != nil {
		panic(err)
	}
}

func importLocal(ctx context.Context, cwd dagger.FS, pkgName string, dockerfile string) {
	output, err := core.Dockerfile(ctx, cwd, dockerfile)
	if err != nil {
		panic(err)
	}
	_, err = core.Import(ctx, pkgName, output.Core.Dockerfile)
	if err != nil {
		panic(err)
	}
}

func importImage(ctx context.Context, pkgName string, ref string) {
	output, err := core.Image(ctx, ref)
	if err != nil {
		panic(err)
	}
	_, err = core.Import(ctx, pkgName, output.Core.Image.Fs)
	if err != nil {
		panic(err)
	}
}
