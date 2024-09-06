package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/identity"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/build"
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type Distro string

const (
	DistroAlpine = "alpine"
	DistroWolfi  = "wolfi"
	DistroUbuntu = "ubuntu"
)

type Engine struct {
	Dagger *DaggerDev // +private

	Args   []string // +private
	Config []string // +private

	Trace bool // +private

	Race         bool                // +private
	GpuSupport   bool                // +private
	Image        *Distro             // +private
	Platform     dagger.Platform     // +private
	CacheVolume  *dagger.CacheVolume // +private
	InstanceName string              // +private
	DockerConfig *dagger.Secret      // +private
}

func (e *Engine) WithConfig(key, value string) *Engine {
	e.Config = append(e.Config, key+"="+value)
	return e
}

func (e *Engine) WithArg(key, value string) *Engine {
	e.Args = append(e.Args, key+"="+value)
	return e
}

func (e *Engine) WithRace() *Engine {
	e.Race = true
	return e
}

func (e *Engine) WithTrace() *Engine {
	e.Trace = true
	return e
}

func (e *Engine) WithGpuSupport(value bool) *Engine {
	e.GpuSupport = value
	return e
}

func (e *Engine) WithImage(image *Distro) *Engine {
	e.Image = image
	return e
}

func (e *Engine) WithPlatform(platform dagger.Platform) *Engine {
	e.Platform = platform
	return e
}

func (e *Engine) WithCacheVolume(volume *dagger.CacheVolume) *Engine {
	e.CacheVolume = e.CacheVolume
	return e
}

// Set an instance name, to spawn different instances of the service, each
// with their own lifecycle and state volume
func (e *Engine) WithInstanceName(name string) *Engine {
	e.InstanceName = name
	return e
}

func (e *Engine) WithDockerConfig(config *dagger.Secret) *Engine {
	e.DockerConfig = config
	return e
}

// Build the engine container
func (e *Engine) Container(ctx context.Context) (*dagger.Container, error) {
	cfg, err := generateConfig(e.Trace, e.Config)
	if err != nil {
		return nil, err
	}
	entrypoint, err := generateEntrypoint(e.Args)
	if err != nil {
		return nil, err
	}

	builder, err := build.NewBuilder(ctx, e.Dagger.Source())
	if err != nil {
		return nil, err
	}
	builder = builder.
		WithVersion(e.Dagger.Version.String()).
		WithTag(e.Dagger.Tag).
		WithRace(e.Race)
	if e.Platform != "" {
		builder = builder.WithPlatform(e.Platform)
	}

	if e.Image != nil {
		switch *e.Image {
		case DistroAlpine:
			builder = builder.WithAlpineBase()
		case DistroWolfi:
			builder = builder.WithWolfiBase()
		case DistroUbuntu:
			builder = builder.WithUbuntuBase()
		default:
			return nil, fmt.Errorf("unknown base image type %s", *e.Image)
		}
	}

	if e.GpuSupport {
		builder = builder.WithGPUSupport()
	}

	ctr, err := builder.Engine(ctx)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		WithFile(engineTomlPath, cfg).
		WithFile(engineEntrypointPath, entrypoint).
		WithEntrypoint([]string{filepath.Base(engineEntrypointPath)})

	cli, err := builder.CLI(ctx)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		WithFile(cliPath, cli).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "unix://"+engineUnixSocketPath)

	return ctr, nil
}

// A sidecar service with zeroconf binding
//type Sidecar interface {
//	DaggerObject
//	Service(context.Context) (*dagger.Service, error)
//	Bind(context.Context, *dagger.Container) (*dagger.Container, error)
//}

// Instantiate the engine as a service, and bind it to the given client
func (e *Engine) Bind(ctx context.Context, client *dagger.Container) (*dagger.Container, error) {
	engineSvc, err := e.Service(ctx)
	if err != nil {
		return nil, err
	}
	cliBinary, err := e.Dagger.CLI().Binary(ctx, "")
	if err != nil {
		return nil, err
	}
	cliBinaryPath := "/.dagger-cli"
	client = client.
		WithServiceBinding("dagger-engine", engineSvc).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dagger-engine:1234").
		WithMountedFile(cliBinaryPath, cliBinary).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinaryPath).
		WithExec([]string{"ln", "-s", cliBinaryPath, "/usr/local/bin/dagger"})
	if e.DockerConfig != nil {
		// this avoids rate limiting in our ci tests
		client = client.WithMountedSecret("/root/.docker/config.json", e.DockerConfig)
	}
	return client, nil
}

func (e *Engine) cacheVolume() *dagger.CacheVolume {
	var name string
	if v := e.Dagger.Version; v != nil {
		name = "dagger-dev-engine-state-" + v.String()
	} else {
		name = "dagger-dev-engine-state-" + identity.NewID()
	}
	if e.InstanceName == "" {
		name += "-" + e.InstanceName
	}
	return dagger.Connect().CacheVolume(name)
}

// Create a test engine service
func (e *Engine) Service(ctx context.Context) (*dagger.Service, error) {
	cacheVolume := e.cacheVolume()
	e = e.
		WithConfig("grpc", `address=["unix:///var/run/buildkit/buildkitd.sock", "tcp://0.0.0.0:1234"]`).
		WithArg(`network-name`, `dagger-dev`).
		WithArg(`network-cidr`, `10.88.0.0/16`)
	devEngine, err := e.Container(ctx)
	if err != nil {
		return nil, err
	}
	devEngine = devEngine.
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithMountedCache(distconsts.EngineDefaultStateDir, cacheVolume, dagger.ContainerWithMountedCacheOpts{
			// only one engine can run off it's local state dir at a time; Private means that we will attempt to re-use
			// these cache volumes if they are not already locked to another running engine but otherwise will create a new
			// one, which gets us best-effort cache re-use for these nested engine services
			Sharing: dagger.Private,
		}).
		WithExec(nil, dagger.ContainerWithExecOpts{
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})

	return devEngine.AsService(), nil
}

// Lint the engine
func (e *Engine) Lint(
	ctx context.Context,
) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		allPkgs, err := e.Dagger.containing(ctx, "go.mod")
		if err != nil {
			return err
		}

		var pkgs []string
		for _, pkg := range allPkgs {
			if strings.HasPrefix(pkg, "docs/") {
				continue
			}
			if strings.HasPrefix(pkg, "core/integration/") {
				continue
			}
			pkgs = append(pkgs, pkg)
		}

		return dag.
			Go(e.Dagger.WithModCodegen().Source()).
			Lint(ctx, dagger.GoLintOpts{Packages: pkgs})
	})
	eg.Go(func() error {
		return e.LintGenerate(ctx)
	})

	return eg.Wait()
}

// Generate any engine-related files
// Note: this is codegen of the 'go generate' variety, not 'dagger develop'
func (e *Engine) Generate() *dagger.Directory {
	generated := e.Dagger.Go().Env().
		WithoutDirectory("sdk") // sdk generation happens separately

	// protobuf dependencies
	generated = generated.
		WithExec([]string{"apk", "add", "protoc=~3.21.12"}).
		WithExec([]string{"go", "install", "google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2"}).
		WithExec([]string{"go", "install", "github.com/gogo/protobuf/protoc-gen-gogoslick@v1.3.2"}).
		WithExec([]string{"go", "install", "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0"})

	generated = generated.
		WithExec([]string{"go", "generate", "-v", "./..."})

	return generated.Directory(".")
}

// Lint any generated engine-related files
func (e *Engine) LintGenerate(ctx context.Context) error {
	before := e.Dagger.Go().Env().WithoutDirectory("sdk").Directory(".")
	after := e.Generate()
	return dag.Dirdiff().AssertEqual(ctx, before, after, []string{"."})
}

var targets = []struct {
	Name       string
	Tag        string
	Image      Distro
	Platforms  []dagger.Platform
	GPUSupport bool
}{
	{
		Name:      "alpine (default)",
		Tag:       "%s",
		Image:     DistroAlpine,
		Platforms: []dagger.Platform{"linux/amd64", "linux/arm64"},
	},
	{
		Name:       "ubuntu with nvidia variant",
		Tag:        "%s-gpu",
		Image:      DistroUbuntu,
		Platforms:  []dagger.Platform{"linux/amd64"},
		GPUSupport: true,
	},
	{
		Name:      "wolfi",
		Tag:       "%s-wolfi",
		Image:     DistroWolfi,
		Platforms: []dagger.Platform{"linux/amd64"},
	},
	{
		Name:       "wolfi with nvidia variant",
		Tag:        "%s-wolfi-gpu",
		Image:      DistroWolfi,
		Platforms:  []dagger.Platform{"linux/amd64"},
		GPUSupport: true,
	},
}

// Publish all engine images to a registry
func (e *Engine) Publish(
	ctx context.Context,

	// Image target to push to
	image string,
	// List of tags to use
	tag []string,

	// +optional
	dryRun bool,

	// +optional
	registry *string,
	// +optional
	registryUsername *string,
	// +optional
	registryPassword *dagger.Secret,
) error {
	// collect all the targets that we are trying to build together, along with
	// where they need to go to
	targetResults := make([]struct {
		Platforms []*dagger.Container
		Tags      []string
	}, len(targets))

	eg, egCtx := errgroup.WithContext(ctx)
	for i, target := range targets {
		// determine the target tags
		for _, tag := range tag {
			targetResults[i].Tags = append(targetResults[i].Tags, fmt.Sprintf(target.Tag, tag))
		}

		// build all the target platforms
		targetResults[i].Platforms = make([]*dagger.Container, len(target.Platforms))
		for j, platform := range target.Platforms {
			egCtx, span := Tracer().Start(egCtx, fmt.Sprintf("building %s [%s]", target.Name, platform))
			eg.Go(func() (rerr error) {
				defer func() {
					if rerr != nil {
						span.SetStatus(codes.Error, rerr.Error())
					}
					span.End()
				}()
				ctr, err := e.
					WithPlatform(platform).
					WithImage(&target.Image).
					WithGpuSupport(target.GPUSupport).
					Container(egCtx)
				if err != nil {
					return err
				}
				ctr, err = ctr.Sync(egCtx)
				if err != nil {
					return err
				}

				targetResults[i].Platforms[j] = ctr
				return nil
			})
		}
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	// push all the targets
	ctr := dag.Container()
	if registry != nil && registryUsername != nil && registryPassword != nil {
		ctr = ctr.WithRegistryAuth(*registry, *registryUsername, registryPassword)
	}
	for i, target := range targets {
		result := targetResults[i]

		if err := func() (rerr error) {
			ctx, span := Tracer().Start(ctx, fmt.Sprintf("pushing %s", target.Name))
			defer func() {
				if rerr != nil {
					span.SetStatus(codes.Error, rerr.Error())
				}
				span.End()
			}()

			for _, tag := range result.Tags {
				_, err := ctr.
					Publish(ctx, fmt.Sprintf("%s:%s", image, tag), dagger.ContainerPublishOpts{
						PlatformVariants:  result.Platforms,
						ForcedCompression: dagger.Gzip, // use gzip to avoid incompatibility w/ older docker versions
					})
				if err != nil {
					return err
				}
			}
			return nil
		}(); err != nil {
			return err
		}
	}

	return nil
}

func (e *Engine) Scan(ctx context.Context) (string, error) {
	target, err := e.Container(ctx)
	if err != nil {
		return "", err
	}

	ignoreFiles := dag.Directory().WithDirectory("/", e.Dagger.Source(), dagger.DirectoryWithDirectoryOpts{
		Include: []string{
			".trivyignore",
			".trivyignore.yml",
			".trivyignore.yaml",
		},
	})
	ignoreFileNames, err := ignoreFiles.Entries(ctx)
	if err != nil {
		return "", err
	}

	// FIXME: trivy module
	ctr := dag.Container().
		From("aquasec/trivy:0.50.4").
		WithMountedFile("/mnt/engine.tar", target.AsTarball()).
		WithMountedDirectory("/mnt/ignores", ignoreFiles).
		WithMountedCache("/root/.cache/", dag.CacheVolume("trivy-cache"))

	args := []string{
		"trivy",
		"image",
		"--format=json",
		"--no-progress",
		"--exit-code=1",
		"--vuln-type=os,library",
		"--severity=CRITICAL,HIGH",
		"--show-suppressed",
	}
	if len(ignoreFileNames) > 0 {
		args = append(args, "--ignorefile=/mnt/ignores/"+ignoreFileNames[0])
	}
	args = append(args, "--input", "/mnt/engine.tar")

	return ctr.WithExec(args).Stdout(ctx)
}
