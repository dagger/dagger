// Package ioctx provides a way to pass standard input, output, and error streams
// through a context.Context.
package ioctx

import (
	"bytes"
	"context"
	"io"
)

type stdinKey struct{}

type stderrKey struct{}

type stdoutKey struct{}

var nopReader = new(bytes.Buffer)

// Stdin returns the standard input stream for the current context.
func Stdin(ctx context.Context) io.Reader {
	val := ctx.Value(stdinKey{})
	if val == nil {
		return nopReader
	}
	return val.(io.Reader)
}

// Stdout returns the standard output stream for the current context.
func Stdout(ctx context.Context) io.Writer {
	val := ctx.Value(stdoutKey{})
	if val == nil {
		return io.Discard
	}
	return val.(io.Writer)
}

// Stderr returns the standard error stream for the current context.
func Stderr(ctx context.Context) io.Writer {
	val := ctx.Value(stderrKey{})
	if val == nil {
		return io.Discard
	}
	return val.(io.Writer)
}

// WithStdin returns a new context with the given standard input stream.
func WithStdin(ctx context.Context, stdin io.Reader) context.Context {
	return context.WithValue(ctx, stdinKey{}, stdin)
}

// WithStdout returns a new context with the given standard output stream.
func WithStdout(ctx context.Context, stdout io.Writer) context.Context {
	return context.WithValue(ctx, stdoutKey{}, stdout)
}

// WithStderr returns a new context with the given standard error stream.
func WithStderr(ctx context.Context, stderr io.Writer) context.Context {
	return context.WithValue(ctx, stderrKey{}, stderr)
}
