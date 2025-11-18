package imageload

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/util/traceexec"
	"go.opentelemetry.io/otel"
)

type Docker struct {
	Cmd string
}

func init() {
	register("docker-image", &Docker{"docker"})
	register("podman-image", &Docker{"podman"})
	register("finch-image", &Docker{"finch"})
	register("nerdctl-image", &Docker{"nerdctl"})
}

func (loader Docker) Loader(ctx context.Context) (*Loader, error) {
	return &Loader{
		TarballWriter: loader.loadTarball,
		TarballReader: loader.saveTarball,
	}, nil
}

func (loader Docker) loadTarball(ctx context.Context, name string, tarball io.Reader) (rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "load "+name)
	defer telemetry.EndWithCause(span, &rerr)

	cmd := exec.CommandContext(ctx, loader.Cmd, "load")
	cmd.Stdin = tarball
	stdout, _, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
	if err != nil {
		return fmt.Errorf("docker load failed: %w", err)
	}

	result := regexp.MustCompile(`\b(sha256:[0-9a-f]+|\S+:\S+)\b`).FindStringSubmatch(stdout)
	if len(result) == 0 {
		return fmt.Errorf("unexpected output from docker load: %s", stdout)
	}
	imageID := result[1]

	err = traceexec.Exec(ctx, exec.CommandContext(ctx, loader.Cmd, "tag", imageID, name), telemetry.Encapsulated())
	if err != nil {
		return fmt.Errorf("docker tag failed: %w", err)
	}

	return nil
}

func (loader Docker) saveTarball(ctx context.Context, name string, tarball io.Writer) (rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "save "+name)
	defer telemetry.EndWithCause(span, &rerr)

	cmd := exec.CommandContext(ctx, "docker", "save", name)
	cmd.Stdout = tarball
	err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated())
	if err != nil {
		return fmt.Errorf("docker save failed: %w", err)
	}

	return nil
}
