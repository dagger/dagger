//go:generate go run github.com/Khan/genqlient ./gen/core/genqlient.yaml
//go:generate go run github.com/Khan/genqlient ./gen/alpine/genqlient.yaml
//go:generate go run github.com/Khan/genqlient ./gen/netlify/genqlient.yaml
//go:generate go run github.com/Khan/genqlient ./gen/yarn/genqlient.yaml
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/cloak/cmd/demo/gen/alpine"
	"github.com/dagger/cloak/cmd/demo/gen/core"
	"github.com/dagger/cloak/cmd/demo/gen/netlify"
	"github.com/dagger/cloak/cmd/demo/gen/yarn"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/sdk/go/dagger"
)

func main() {
	startOpts := &engine.StartOpts{
		LocalDirs: map[string]string{
			".": ".",
		},
		/*
			Export: &client.ExportEntry{
				Type:      client.ExporterLocal,
				OutputDir: "./out",
			},
		*/
	}

	err := engine.Start(context.Background(), startOpts,
		func(ctx context.Context, localDirs map[string]dagger.FS) (*dagger.FS, error) {
			importLocal(ctx, localDirs["."], "alpine", "Dockerfile.alpine")
			importLocal(ctx, localDirs["."], "netlify", "Dockerfile.netlify")
			importLocal(ctx, localDirs["."], "yarn", "Dockerfile.yarn")

			base, err := alpine.Build(ctx, []string{"jq", "curl"})
			if err != nil {
				return nil, err
			}

			netlifyOutput, err := netlify.Deploy(ctx, base.Alpine.Build, "site", "token")
			if err != nil {
				return nil, err
			}

			fmt.Printf("%+v\n", netlifyOutput.Netlify.Deploy)

			yarnOutput, err := yarn.Script(ctx, base.Alpine.Build, "somescript")
			if err != nil {
				return nil, err
			}

			fmt.Printf("%+v\n", yarnOutput.Yarn.Script)

			/*
				if err := engine.Shell(ctx, output.Alpine.Build); err != nil {
					return nil, err
				}
			*/

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
