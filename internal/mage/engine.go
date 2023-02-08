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
	runcRepo      = "github.com/opencontainers/runc"
	runcRef       = "v1.1.4"
	buildkitRepo  = "github.com/moby/buildkit"
	buildkitRef   = "v0.11.1"
	qemuBinImage  = "tonistiigi/binfmt:buildkit-v7.1.0-30@sha256:45dd57b4ba2f24e2354f71f1e4e51f073cb7a28fd848ce6f5f2a7701142a6bf0"

	engineTomlPath = "/etc/dagger/engine.toml"
	// NOTE: this needs to be consistent with DefaultStateDir in internal/engine/docker.go
	engineDefaultStateDir = "/var/lib/dagger"

	engineEntrypointPath    = "/usr/local/bin/dagger-entrypoint.sh"
	engineEntrypointCommand = "/usr/local/bin/" + engineBinName + " --debug --config " + engineTomlPath + " --oci-worker-binary /usr/local/bin/" + shimBinName

	cacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
)

// setting cgroup v2 nesting. ref: https://github.com/moby/moby/blob/38805f20f9bcc5e87869d6c79d432b166e1c88b4/hack/dind#L28
var engineEntrypoint = fmt.Sprintf(`#!/bin/sh
set -e

# cgroup v2: enable nesting
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
	# move the processes from the root group to the /init group,
	# otherwise writing subtree_control fails with EBUSY.
	# An error during moving non-existent process (i.e., "cat") is ignored.
	mkdir -p /sys/fs/cgroup/init
	xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs || :
	# enable controllers
	sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers \
		> /sys/fs/cgroup/cgroup.subtree_control
fi

exec %s
`, engineEntrypointCommand)

var engineToml = fmt.Sprintf("root = %q\n", engineDefaultStateDir)

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

// Build builds the engine binary
func (t Engine) Build(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()
	c = c.Pipeline("engine").Pipeline("build")
	build := util.GoBase(c).
		WithEnvVariable("GOOS", runtime.GOOS).
		WithEnvVariable("GOARCH", runtime.GOARCH).
		WithExec([]string{"go", "build", "-o", "./bin/dagger", "-ldflags", "-s -w", "/app/cmd/dagger"})

	_, err = build.Directory("./bin").Export(ctx, "./bin")
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

	// Ensure buildkitd and client (go.mod) are the same version.
	goMod, err := repo.File("go.mod").Contents(ctx)
	if err != nil {
		return err
	}
	for _, l := range strings.Split(goMod, "\n") {
		l = strings.TrimSpace(l)
		parts := strings.SplitN(l, " ", 2)
		if len(parts) != 2 {
			continue
		}
		repo, version := parts[0], parts[1]
		if repo != buildkitRepo {
			continue
		}
		if version != buildkitRef {
			return fmt.Errorf("buildkit version mismatch: %s (buildkitd) != %s (buildkit in go.mod)", buildkitRef, version)
		}
	}

	_, err = c.Container().
		From("golangci/golangci-lint:v1.48").
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

	arches := []string{"amd64", "arm64"}

	digest, err := c.Container().Publish(ctx, ref, dagger.ContainerPublishOpts{
		PlatformVariants: devEngineContainer(c, arches),
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

func (t Engine) test(ctx context.Context, race bool) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	c = c.Pipeline("engine").Pipeline("test")

	cgoEnabledEnv := "0"
	args := []string{"go", "test", "-p", "16", "-v", "-count=1"}
	if race {
		args = append(args, "-race", "-timeout=1h")
		cgoEnabledEnv = "1"
	}
	args = append(args, "./...")

	output, err := util.GoBase(c).
		WithMountedDirectory("/app", util.Repository(c)). // need all the source for extension tests
		WithWorkdir("/app").
		WithEnvVariable("CGO_ENABLED", cgoEnabledEnv).
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
		PlatformVariants: devEngineContainer(c, arches),
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

	if output, err := exec.CommandContext(ctx, "docker",
		"run",
		"-d",
		"--rm",
		"-e", cacheConfigEnvName,
		"-v", volumeName+":"+engineDefaultStateDir,
		"--name", util.EngineContainerName,
		"--privileged",
		imageName,
		"--debug",
	).CombinedOutput(); err != nil {
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

func devEngineContainer(c *dagger.Client, arches []string) []*dagger.Container {
	platformVariants := make([]*dagger.Container, 0, len(arches))
	for _, arch := range arches {
		platformVariants = append(platformVariants, c.
			Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
			From("alpine:"+alpineVersion).
			WithExec([]string{"apk", "add", "git", "openssh", "pigz", "xz"}).
			WithFile("/usr/local/bin/runc", runcBin(c, arch)).
			WithFile("/usr/local/bin/buildctl", buildctlBin(c, arch)).
			WithFile("/usr/local/bin/"+shimBinName, shimBin(c, arch)).
			WithFile("/usr/local/bin/"+engineBinName, engineBin(c, arch)).
			WithDirectory("/usr/local/bin", qemuBins(c, arch)).
			WithDirectory(engineDefaultStateDir, c.Directory()).
			WithNewFile(engineTomlPath, dagger.ContainerWithNewFileOpts{
				Contents: engineToml,
			}).
			WithNewFile(engineEntrypointPath, dagger.ContainerWithNewFileOpts{
				Contents:    engineEntrypoint,
				Permissions: 755,
			}).
			WithEntrypoint([]string{
				"dagger-entrypoint.sh",
			}),
		)
	}

	return platformVariants
}

func engineBin(c *dagger.Client, arch string) *dagger.File {
	return util.GoBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/" + engineBinName,
			"-ldflags", "-s -w",
			"/app/cmd/engine",
		}).
		File("./bin/" + engineBinName)
}

func shimBin(c *dagger.Client, arch string) *dagger.File {
	return util.GoBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/" + shimBinName,
			"-ldflags", "-s -w",
			"/app/cmd/shim",
		}).
		File("./bin/" + shimBinName)
}

func buildctlBin(c *dagger.Client, arch string) *dagger.File {
	return util.GoBase(c).
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithMountedDirectory("/app", c.Git(buildkitRepo).Branch(buildkitRef).Tree()).
		WithExec([]string{
			"go", "build",
			"-o", "./bin/buildctl",
			"-ldflags", "-s -w",
			"/app/cmd/buildctl",
		}).
		File("./bin/buildctl")
}

func runcBin(c *dagger.Client, arch string) *dagger.File {
	return util.GoBase(c).
		WithExec([]string{"apk", "add", "musl-dev", "gcc", "libseccomp-dev", "libseccomp-static"}).
		WithEnvVariable("CGO_ENABLED", "1").
		WithEnvVariable("GOOS", "linux").
		WithEnvVariable("GOARCH", arch).
		WithMountedDirectory("/app", c.Git(runcRepo).Branch(runcRef).Tree()).
		WithExec([]string{
			"go", "build",
			"-ldflags", "-extldflags -static",
			"-tags", "apparmor seccomp netgo cgo static_build osusergo",
			"-o", "./bin/runc",
		}).File("./bin/runc")
}

func qemuBins(c *dagger.Client, arch string) *dagger.Directory {
	return c.
		Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/" + arch)}).
		From(qemuBinImage).
		Rootfs()
}
