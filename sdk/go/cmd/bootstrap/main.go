package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path"

	"dagger.io/dagger"
	"github.com/iancoleman/strcase"
	"github.com/spf13/cobra"
	"golang.org/x/exp/slog"
)

var (
	modRoot    string
	modSubPath string
	publish    string
	export     string
)

//go:embed help.txt
var help string

var rootCmd = &cobra.Command{
	Use:   "bootstrap",
	RunE:  Bootstrap,
	Short: `Bootstraps a Dagger SDK runtime module.`,
	Long:  help,
}

var supportedPlatforms = []dagger.Platform{"linux/amd64", "linux/arm64"}

func init() {
	rootCmd.Flags().StringVar(&modRoot, "root", ".",
		"Root directory of the module.")

	rootCmd.Flags().StringVar(&modSubPath, "subpath", "./runtime",
		"Subpath of the module within the root directory.")

	rootCmd.Flags().StringVar(&publish, "publish", "",
		"Publish the image to a registry at the given address.")

	rootCmd.Flags().StringVar(&export, "export", "",
		"Export the image to a tarball at the given path.")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func Bootstrap(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}

	modSrcRoot := dag.Host().Directory(modRoot, dagger.HostDirectoryOpts{
		// Don't bust our own cache.
		Include: []string{
			// base client for bootstrapping/codegen
			"./go.mod",
			"./go.sum",
			"./*.go",
			"./internal/",

			// utilities for loading modules
			"./modules/",

			// codegen, for generating the runtime module
			"./codegen/",
			"./cmd/codegen/main.go",

			// runtime code, ignoring generated files
			"./runtime/main.go",
			"./runtime/generate.go",
			"./runtime/dagger.json",
		},
	})

	// first, build the SDK's runtime container using the client
	sdkRuntime := bootstrap(dag, modSrcRoot, modSubPath)

	// next, build the SDK's runtime container using its own container
	runtimeVariants, err := bootstrapUsingModule(ctx, dag, modSrcRoot, modSubPath, sdkRuntime)
	if err != nil {
		return fmt.Errorf("bootstrap using module: %w", err)
	}

	if export != "" {
		if _, err := dag.Container().Export(ctx, export, dagger.ContainerExportOpts{
			PlatformVariants: runtimeVariants,
		}); err != nil {
			return err
		}

		slog.Info("container exported", "dest", export)
	}

	if publish != "" {
		slog.Info("publishing container", "ref", publish)

		addr, err := dag.Container().Publish(ctx, publish, dagger.ContainerPublishOpts{
			PlatformVariants: runtimeVariants,
		})
		if err != nil {
			return err
		}

		slog.Info("container published", "ref", addr)
	}

	return nil
}

const (
	// modSourceDirPath is the path that we'll mount the SDK code during bootstrapping.
	//
	// This is not an external contract.
	modSourceDirPath = "/sdk"

	// runtimeExecutablePath is the path to the runtime executable within the SDK container.
	//
	// This is not an external contract; it gets set as the container entrypoint.
	runtimeExecutablePath = "/runtime"
)

// bootstrap builds the module "natively" using the Dagger client.
//
// This approximates the runtime module's ModuleRuntime code; unfortunately we
// can't share code because the runtime module has its own types.
//
// Fortunately, this container is shortlived: we only use it to build the
// runtime again, using the module, so the module is always the source of
// truth.
func bootstrap(dag *dagger.Client, modSrcRoot *dagger.Directory, subPath string) *dagger.Container {
	modSubPath := path.Join(modSourceDirPath, modSubPath)
	return dag.Container().
		From("golang:1.21-alpine").
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("modgomodcache")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("modgobuildcache")).
		WithDirectory(modSourceDirPath, modSrcRoot).
		WithWorkdir(modSubPath).
		// run codegen for the runtime module
		WithExec([]string{"go", "generate", "-x", "."}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		WithExec([]string{
			"go", "build",
			"-o", runtimeExecutablePath,
			"-ldflags", "-s -d -w",
			".",
		}).
		WithWorkdir(modSourceDirPath).
		WithEntrypoint([]string{runtimeExecutablePath}).
		WithLabel("io.dagger.module.config", modSubPath)
}

// bootstrapUsingModule invokes the module we just bootstrapped and tells it to
// build itself on all supported platforms.
func bootstrapUsingModule(
	ctx context.Context,
	dag *dagger.Client,
	modSrcRoot *dagger.Directory,
	modSubPath string,
	sdkRuntime *dagger.Container,
) ([]*dagger.Container, error) {
	sdkMod := modSrcRoot.AsModule(dagger.DirectoryAsModuleOpts{
		Runtime:       sdkRuntime,
		SourceSubpath: modSubPath,
	})

	if _, err := sdkMod.Serve(ctx); err != nil {
		return nil, fmt.Errorf("serve SDK module: %w", err)
	}

	modName, err := sdkMod.Name(ctx)
	if err != nil {
		return nil, err
	}

	modSrcRootID, err := modSrcRoot.ID(ctx)
	if err != nil {
		return nil, err
	}

	type CallResult struct {
		ModuleRuntime struct {
			ID dagger.ContainerID
		}
	}

	variants := make([]*dagger.Container, 0, len(supportedPlatforms))
	for _, platform := range supportedPlatforms {
		res := map[string]CallResult{}
		modSelector := strcase.ToLowerCamel(modName)
		err = dag.Do(ctx, &dagger.Request{
			Query: fmt.Sprintf(`
				query Bootstrap($platform: String!, $modSource: DirectoryID!, $modSubpath: String!) {
					%s {
						moduleRuntime(platform: $platform, modSource: $modSource, subPath: $modSubpath) {
							id
						}
					}
				}
			`, modSelector),
			Variables: map[string]interface{}{
				"platform":   platform,
				"modSource":  modSrcRootID,
				"modSubpath": modSubPath,
			},
		}, &dagger.Response{
			Data: &res,
		})
		if err != nil {
			return nil, err
		}

		containerID := res[modSelector].ModuleRuntime.ID
		if containerID == "" {
			return nil, fmt.Errorf("moduleRuntime returned empty container ID")
		}

		variant := dag.Container(dagger.ContainerOpts{
			ID: containerID,
		})

		variant, err := variant.Sync(ctx)
		if err != nil {
			return nil, fmt.Errorf("platform %s: %w", platform, err)
		}

		variants = append(variants, variant)
	}

	return variants, nil
}
