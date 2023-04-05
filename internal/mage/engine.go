package mage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/sdk"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"golang.org/x/mod/semver"
)

const (
	engineBinName = "dagger-engine"
	shimBinName   = "dagger-shim"
	alpineVersion = "3.17"
	runcVersion   = "v1.1.5"
	qemuBinImage  = "tonistiigi/binfmt:buildkit-v7.1.0-30@sha256:45dd57b4ba2f24e2354f71f1e4e51f073cb7a28fd848ce6f5f2a7701142a6bf0"

	// NOTE: this needs to be consistent with DefaultStateDir in internal/engine/docker.go
	engineDefaultStateDir = "/var/lib/dagger"

	cacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
	servicesDNSEnvName = "_EXPERIMENTAL_DAGGER_SERVICES_DNS"
)

var publishedEngineArches = []string{"amd64", "arm64"}

func parseRef(tag string) error {
	if tag == "main" {
		return nil
	}
	if ok := semver.IsValid(tag); !ok {
		return fmt.Errorf("invalid semver tag: %s", tag)
	}
	return nil
}

type Engine mg.Namespace

// Build builds the dagger cli binary
func (t Engine) Build(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()
	c = c.Pipeline("engine").Pipeline("build")

	_, err = util.HostDaggerBinary(c).Export(ctx, "./bin/dagger")

	return err
}

// Lint lints the engine
func (t Engine) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("engine").Pipeline("lint")

	repo := util.RepositoryGoCodeOnly(c)

	_, err = c.Container().
		From("golangci/golangci-lint:v1.51").
		WithMountedDirectory("/app", repo).
		WithWorkdir("/app").
		WithExec([]string{"golangci-lint", "run", "-v", "--timeout", "5m"}).
		ExitCode(ctx)
	return err
}

// Publish builds and pushes Engine OCI image to a container registry
func (t Engine) Publish(ctx context.Context, version string) error {
	if err := parseRef(version); err != nil {
		return err
	}

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("engine").Pipeline("publish")
	engineImage, err := util.WithSetHostVar(ctx, c.Host(), "DAGGER_ENGINE_IMAGE").Value(ctx)
	if err != nil {
		return err
	}
	ref := fmt.Sprintf("%s:%s", engineImage, version)

	digest, err := c.Container().Publish(ctx, ref, dagger.ContainerPublishOpts{
		PlatformVariants: util.DevEngineContainer(c, publishedEngineArches),
	})
	if err != nil {
		return err
	}

	if semver.IsValid(version) {
		sdks := sdk.All{}
		if err := sdks.Bump(ctx, version); err != nil {
			return err
		}
	} else {
		fmt.Printf("'%s' is not a semver version, skipping image bump in SDKs", version)
	}

	time.Sleep(3 * time.Second) // allow buildkit logs to flush, to minimize potential confusion with interleaving
	fmt.Println("PUBLISHED IMAGE REF:", digest)

	return nil
}

// Verify that all arches for the engine can be built. Just do a local export to avoid setting up
// a registry
func (t Engine) TestPublish(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("engine").Pipeline("test-publish")
	_, err = c.Container().Export(ctx, "./engine.tar.gz", dagger.ContainerExportOpts{
		PlatformVariants: util.DevEngineContainer(c, publishedEngineArches),
	})
	return err
}

func registry(c *dagger.Client) *dagger.Container {
	return c.Pipeline("registry").Container().From("registry:2").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithExec(nil)
}

func privateRegistry(c *dagger.Client) *dagger.Container {
	const htpasswd = "john:$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC" //nolint:gosec
	return c.Pipeline("private registry").Container().From("registry:2").
		WithNewFile("/auth/htpasswd", dagger.ContainerWithNewFileOpts{Contents: htpasswd}).
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		WithExec(nil)
}

func (t Engine) test(ctx context.Context, race bool) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("engine").Pipeline("test")

	opts := util.DevEngineOpts{
		ConfigEntries: map[string]string{
			`registry."registry:5000"`:        "http = true",
			`registry."privateregistry:5000"`: "http = true",
		},
	}
	devEngine := util.DevEngineContainer(c.Pipeline("dev-engine"), []string{runtime.GOARCH}, util.DefaultDevEngineOpts, opts)[0]
	devEngine = devEngine.
		WithServiceBinding("registry", registry(c)).
		WithServiceBinding("privateregistry", privateRegistry(c)).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.Tcp}).
		// TODO: in some ways it's nice to have cache here, in others it may actually result in our tests being less reproducible.
		// Can consider rm -rfing this dir every engine start if we decide we want a clean slate every time.
		// It's important it's a cache mount though because otherwise overlay won't be available
		WithMountedCache("/var/lib/dagger", c.CacheVolume("dagger-dev-engine-state")).
		WithExec(nil, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities:      true,
			ExperimentalPrivilegedNesting: true,
		})

	endpoint, err := devEngine.Endpoint(ctx, dagger.ContainerEndpointOpts{Port: 1234, Scheme: "tcp"})
	if err != nil {
		return err
	}

	cgoEnabledEnv := "0"
	args := []string{"go", "test", "-p", "16", "-v", "-count=1", "-timeout=15m"}
	if race {
		args = append(args, "-race", "-timeout=1h")
		cgoEnabledEnv = "1"
	}
	args = append(args, "./...")
	cliBinPath := "/.dagger-cli"

	output, err := util.GoBase(c).
		WithMountedDirectory("/app", util.Repository(c)). // need all the source for extension tests
		WithWorkdir("/app").
		WithServiceBinding("dagger-engine", devEngine).
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CACHE_CONFIG", os.Getenv("_EXPERIMENTAL_DAGGER_CACHE_CONFIG")).
		WithMountedFile(cliBinPath, util.DaggerBinary(c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", cliBinPath).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", endpoint).
		WithMountedDirectory("/root/.docker", util.HostDockerDir(c)).
		WithExec(args).
		Stdout(ctx)
	if err != nil {
		return err
	}
	fmt.Println(output)
	return nil
}

// Test runs Engine tests
func (t Engine) Test(ctx context.Context) error {
	return t.test(ctx, false)
}

// TestRace runs Engine tests with go race detector enabled
func (t Engine) TestRace(ctx context.Context) error {
	return t.test(ctx, true)
}

func (t Engine) Dev(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("engine").Pipeline("dev")

	tmpfile, err := os.CreateTemp("", "dagger-engine-export")
	if err != nil {
		return err
	}
	defer os.Remove(tmpfile.Name())

	arches := []string{runtime.GOARCH}

	_, err = c.Container().Export(ctx, tmpfile.Name(), dagger.ContainerExportOpts{
		PlatformVariants: util.DevEngineContainer(c, arches),
	})
	if err != nil {
		return err
	}

	volumeName := util.EngineContainerName
	imageName := fmt.Sprintf("localhost/%s:latest", util.EngineContainerName)

	// #nosec
	loadCmd := exec.CommandContext(ctx, "docker", "load", "-i", tmpfile.Name())
	output, err := loadCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker load failed: %w: %s", err, output)
	}
	_, imageID, ok := strings.Cut(string(output), "sha256:")
	if !ok {
		return fmt.Errorf("unexpected output from docker load: %s", output)
	}
	imageID = strings.TrimSpace(imageID)

	if output, err := exec.CommandContext(ctx, "docker",
		"tag",
		imageID,
		imageName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("docker tag: %w: %s", err, output)
	}

	if output, err := exec.CommandContext(ctx, "docker",
		"rm",
		"-fv",
		util.EngineContainerName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("docker rm: %w: %s", err, output)
	}

	runArgs := []string{
		"run",
		"-d",
		// "--rm",
		"-e", cacheConfigEnvName,
		"-e", servicesDNSEnvName,
		"-v", volumeName + ":" + engineDefaultStateDir,
		"--name", util.EngineContainerName,
		"--privileged",
	}
	runArgs = append(runArgs, imageName, "--debug")

	if output, err := exec.CommandContext(ctx, "docker", runArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("docker run: %w: %s", err, output)
	}

	// build the CLI and export locally so it can be used to connect to the engine
	binDest := filepath.Join(os.Getenv("DAGGER_SRC_ROOT"), "bin", "dagger")
	_, err = util.HostDaggerBinary(c).Export(ctx, binDest)
	if err != nil {
		return err
	}

	fmt.Println("export _EXPERIMENTAL_DAGGER_CLI_BIN=" + binDest)
	fmt.Println("export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://" + util.EngineContainerName)
	return nil
}
