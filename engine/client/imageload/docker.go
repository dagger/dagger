package imageload

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/util/traceexec"
	"go.opentelemetry.io/otel"
)

type Docker struct{}

func init() {
	register("docker-image", &Docker{})
}

func (Docker) ID() string {
	return "docker"
}

func (loader Docker) Loader(ctx context.Context) (_ *Loader, rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "check for docker daemon")
	defer telemetry.End(span, func() error { return rerr })

	// check docker is running
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	_, err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated())
	if err != nil {
		return nil, err
	}

	return &Loader{
		TarballLoader: loader.loadTarball,
	}, nil
}

func (loader Docker) loadTarball(ctx context.Context, name string, tarball io.Reader) (rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "load "+name)
	defer telemetry.End(span, func() error { return rerr })

	cmd := exec.CommandContext(ctx, "docker", "load")
	cmd.Stdin = tarball
	stdout, err := traceexec.Exec(ctx, cmd, telemetry.Encapsulated())
	if err != nil {
		return fmt.Errorf("docker load failed: %w", err)
	}

	_, imageID, ok := strings.Cut(stdout, "Loaded image ID: sha256:")
	if !ok {
		_, imageID, ok = strings.Cut(stdout, "Loaded image: sha256:") // podman
		if !ok {
			return fmt.Errorf("unexpected output from docker load")
		}
	}
	imageID = strings.TrimSpace(imageID)

	_, err = traceexec.Exec(ctx, exec.CommandContext(ctx, "docker", "tag", imageID, name), telemetry.Encapsulated())
	if err != nil {
		return fmt.Errorf("docker tag failed: %w", err)
	}

	return nil
}
