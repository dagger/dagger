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

type Apple struct{}

func init() {
	register("apple-image", &Apple{})
}

func (loader Apple) Loader(ctx context.Context) (*Loader, error) {
	return &Loader{
		TarballLoader: loader.loadTarball,
	}, nil
}

func (loader Apple) loadTarball(ctx context.Context, name string, tarball io.Reader) (rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, "load "+name)
	defer telemetry.End(span, func() error { return rerr })

	cmd := exec.CommandContext(ctx, "container", "image", "load")
	cmd.Stdin = tarball
	stdout, _, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
	if err != nil {
		return fmt.Errorf("docker load failed: %w", err)
	}

	result := regexp.MustCompile("sha256:([0-9a-f]+)").FindStringSubmatch(stdout)
	if len(result) == 0 {
		return fmt.Errorf("unexpected output from docker load: %s", stdout)
	}
	imageID := result[1]

	err = traceexec.Exec(ctx, exec.CommandContext(ctx, "container", "image", "tag", imageID, name), telemetry.Encapsulated())
	if err != nil {
		return fmt.Errorf("docker tag failed: %w", err)
	}

	return nil
}
