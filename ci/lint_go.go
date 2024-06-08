package main

import (
	"context"
	"encoding/json"
)

// A go linter
type GoLint struct{}

// Lint a go codebae
func (gl GoLint) Lint(
	ctx context.Context,
	// The Go source directory to lint
	source *Directory,
) (*LintReport, error) {
	config := dag.CurrentModule().Source().File("default-golangci.yml")
	cmd := []string{
		"golangci-lint", "run",
		"-v",
		"--timeout", "5m",
		// Disable limits, we can filter the report instead
		"--max-issues-per-linter", "0",
		"--max-same-issues", "0",
		"--out-format", "json",
		"--issues-exit-code", "0",
		"./...",
	}
	llreportFile := dag.
		Container().
		From(golangCiLintImage).
		WithFile("/etc/golangci.yml", config).
		WithEnvVariable("GOLANGCI_LINT_CONFIG", "/etc/golangci.yml").
		WithMountedDirectory("/src", source).
		WithWorkdir("/src").
		WithExec(cmd, ContainerWithExecOpts{RedirectStdout: "golangci-lint-report.json"}).
		File("golangci-lint-report.json")
	llreportJSON, err := llreportFile.Contents(ctx)
	if err != nil {
		return nil, err
	}
	// Unmarshal the low-level report from linting tool
	var llreport struct {
		Issues []struct {
			Text        string   `json:"Text"`
			FromLinter  string   `json:"FromLinter"`
			SourceLines []string `json:"SourceLines"`
			Replacement *struct {
				Text string `json:"Text"`
			} `json:"Replacement,omitempty"`
			Pos struct {
				Filename string `json:"Filename"`
				Offset   int    `json:"Offset"`
				Line     int    `json:"Line"`
				Column   int    `json:"Column"`
			} `json:"Pos"`
			ExpectedNoLint bool   `json:"ExpectedNoLint"`
			Severity       string `json:"Severity"`
		} `json:"Issues"`
	}
	if err := json.Unmarshal([]byte(llreportJSON), &llreport); err != nil {
		return nil, err
	}
	report := &LintReport{
		Issues:   make([]LintIssue, len(llreport.Issues)),
		LLReport: llreportFile, // Keep the low-level report just in case
	}
	for i := range llreport.Issues {
		report.Issues[i].Text = llreport.Issues[i].Text
		if llreport.Issues[i].Severity == "error" {
			report.Issues[i].IsError = true
		}
		report.Issues[i].Tool = "golangci-lint"
	}
	return report, nil
}
