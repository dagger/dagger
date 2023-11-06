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
	severity Optional[string],
	exitCode Optional[int],
	format Optional[string],
) (string, error) {
	sv := severity.GetOr("UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL")
	ec := exitCode.GetOr(0)
	ft := format.GetOr("table")
	return dag.
		Container().
		From("aquasec/trivy:latest").
		WithExec([]string{"image", "--quiet", "--severity", sv, "--exit-code", strconv.Itoa(ec), "--format", ft, imageRef}).Stdout(ctx)
}
