package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

type ScanResult struct {
	Containers []*dagger.Container `json:"targets"`
	Report     ScanReport
}

type ScanReport struct {
	Contents string   `json:"contents"`
	Authors  []string `json:"Authors"`
}

func (m *Test) Scan() ScanResult {
	return ScanResult{
		Containers: []*dagger.Container{
			dag.Container().From("alpine:3.22.1").WithExec([]string{"echo", "hello world"}),
		},
		Report: ScanReport{
			Contents: "hello world",
			Authors:  []string{"foo", "bar"},
		},
	}
}
