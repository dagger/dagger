package main

import (
	"context"
)

type MyModule struct{}

// Vulnerability severity levels
type Severity string

const (
	// Undetermined risk; analyze further.
	Unknown Severity = "UNKNOWN"

	// Minimal risk; routine fix.
	Low Severity = "LOW"

	// Moderate risk; timely fix.
	Medium Severity = "MEDIUM"

	// Serious risk; quick fix needed.
	High Severity = "HIGH"

	// Severe risk; immediate action.
	Critical Severity = "CRITICAL"
)

func (m *MyModule) Scan(ctx context.Context, ref string, severity Severity) (string, error) {
	ctr := dag.Container().From(ref)

	return dag.Container().
		From("aquasec/trivy:0.50.4").
		WithMountedFile("/mnt/ctr.tar", ctr.AsTarball()).
		WithMountedCache("/root/.cache", dag.CacheVolume("trivy-cache")).
		WithExec([]string{
			"trivy",
			"image",
			"--format=json",
			"--no-progress",
			"--exit-code=1",
			"--vuln-type=os,library",
			"--severity=" + string(severity),
			"--show-suppressed",
			"--input=/mnt/ctr.tar",
		}).Stdout(ctx)
}
