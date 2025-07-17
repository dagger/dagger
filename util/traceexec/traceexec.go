package traceexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func Exec(ctx context.Context, cmd *exec.Cmd, opts ...trace.SpanStartOption) (out string, rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, fmt.Sprintf("exec %s", strings.Join(cmd.Args, " ")), opts...)
	defer telemetry.End(span, func() error { return rerr })
	stdio := telemetry.SpanStdio(ctx, "")
	defer stdio.Close()
	outBuf := new(bytes.Buffer)
	cmd.Stdout = io.MultiWriter(stdio.Stdout, outBuf)
	cmd.Stderr = io.MultiWriter(stdio.Stderr, outBuf)

	err := cmd.Run()
	out = strings.TrimSpace(outBuf.String())
	if err != nil {
		return out, fmt.Errorf("failed to run command: %w", err)
	}
	return out, nil
}
