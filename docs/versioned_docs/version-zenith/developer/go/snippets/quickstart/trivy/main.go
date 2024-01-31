package main

import (
	"context"
	"strconv"
)

type Trivy struct{}

// pull the official Trivy image
// send the trivy CLI an image ref to scan
func (t *Trivy) ScanImage(
	ctx context.Context,
	imageRef string,
	// +optional
	// +default=UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL
	severity string,
	// +optional
	exitCode int,
	// +optional
	// +default=table
	format string,
) (string, error) {
	return dag.
		Container().
		From("aquasec/trivy:latest").
		WithExec([]string{
			"image",
			"--quiet",
			"--severity", severity,
			"--exit-code", strconv.Itoa(exitCode),
			"--format", format,
			imageRef,
		}).
		Stdout(ctx)
}
