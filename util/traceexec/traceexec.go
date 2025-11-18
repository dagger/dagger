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

func Exec(ctx context.Context, cmd *exec.Cmd, opts ...trace.SpanStartOption) error {
	_, _, err := ExecOutput(ctx, cmd, opts...)
	return err
}

func ExecOutput(ctx context.Context, cmd *exec.Cmd, opts ...trace.SpanStartOption) (stdout string, stderr string, rerr error) {
	ctx, span := otel.Tracer("").Start(ctx, fmt.Sprintf("exec %s", strings.Join(cmd.Args, " ")), opts...)
	defer telemetry.EndWithCause(span, &rerr)
	stdio := telemetry.SpanStdio(ctx, "")
	defer stdio.Close()
	outBuf := new(bytes.Buffer)
	if cmd.Stdout == nil {
		cmd.Stdout = io.MultiWriter(stdio.Stdout, outBuf)
	}
	errBuf := new(bytes.Buffer)
	if cmd.Stderr == nil {
		cmd.Stderr = io.MultiWriter(stdio.Stderr, errBuf)
	}

	err := cmd.Run()
	stdout = strings.TrimSpace(outBuf.String())
	stderr = strings.TrimSpace(errBuf.String())
	if err != nil {
		return stdout, stderr, fmt.Errorf("failed to run command %s: %w", cmd.Args, err)
	}
	return stdout, stderr, nil
}
