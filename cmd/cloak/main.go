//go:generate go run github.com/Khan/genqlient ./gen/todoapp/genqlient.yaml
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/cloak/cmd/cloak/gen/core"
	"github.com/dagger/cloak/cmd/cloak/gen/todoapp"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
)

const netlifyTokenID = "netlify-token"

// TODO: convert to cli wrapper
func main() {
	netlifyToken := os.Getenv("NETLIFY_AUTH_TOKEN")
	if netlifyToken == "" {
		fmt.Fprintf(os.Stderr, "missing %s environment variable\n", "NETLIFY_AUTH_TOKEN")
		os.Exit(1)
	}

	startOpts := &engine.StartOpts{
		LocalDirs: map[string]string{
			".":   ".",
			"src": "./examples/todoapp/app",
		},
		Secrets: map[string]string{
			netlifyTokenID: os.Getenv("NETLIFY_AUTH_TOKEN"),
		},
	}

	err := engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS, secrets map[string]string) (*dagger.FS, error) {
			importLocal(ctx, localDirs["."], "alpine", "Dockerfile.alpine")
			importLocal(ctx, localDirs["."], "netlify", "Dockerfile.netlify")
			importLocal(ctx, localDirs["."], "yarn", "Dockerfile.yarn")
			importLocal(ctx, localDirs["."], "todoapp", "Dockerfile.todoapp")

			output, err := todoapp.Deploy(ctx, localDirs["src"], secrets[netlifyTokenID])
			if err != nil {
				return nil, err
			}
			fmt.Printf("%+v\n", output.Todoapp)

			return nil, nil
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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
