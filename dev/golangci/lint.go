package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

const (
	lintImageRepo   = "docker.io/golangci/golangci-lint"
	lintImageTag    = "v1.59-alpine"
	lintImageDigest = "sha256:2a5293b5d25319a515db44f00c7e72466a78488106fbb995730580ef25fb8b20"
	lintImage       = lintImageRepo + ":" + lintImageTag + "@" + lintImageDigest
)

// Lint a go codebase
func (gl Golangci) Lint(
	// The Go source directory to lint
	source *Directory,
	// Lint a specific path within the source directory
	// +optional
	path string,
) LintRun {
	return LintRun{Source: source, Path: path}
}

// The result of running the GolangCI lint tool
type LintRun struct {
	// +private
	Source *Directory
	// +private
	Path string
}

func (run LintRun) Issues(ctx context.Context) ([]Issue, error) {
	report, err := run.parseReport(ctx)
	if err != nil {
		return nil, err
	}
	return report.Issues, nil
}

func (run LintRun) Assert(ctx context.Context) error {
	issues, err := run.Issues(ctx)
	if err != nil {
		return err
	}
	var (
		errCount  int
		summaries []string
	)
	for _, iss := range issues {
		if iss.Severity != "error" {
			continue
		}
		errCount += 1
		summaries = append(summaries, iss.Summary())
	}
	if errCount > 0 {
		return fmt.Errorf("Linting failed with %d issues:\n%s",
			errCount,
			strings.Join(summaries, "\n"),
		)
	}
	return nil
}

func (run LintRun) ErrorCount(ctx context.Context) (int, error) {
	var count int
	issues, err := run.Issues(ctx)
	if err != nil {
		return count, err
	}
	for _, issue := range issues {
		if issue.IsError() {
			count += 1
		}
	}
	return count, nil
}

func (issue Issue) IsError() bool {
	return issue.Severity == "error" || issue.Severity == ""
}

func (run LintRun) WarningCount(ctx context.Context) (int, error) {
	var count int
	issues, err := run.Issues(ctx)
	if err != nil {
		return count, err
	}
	for _, issue := range issues {
		if !issue.IsError() {
			count += 1
		}
	}
	return count, nil
}

// Return a JSON report file for this run
func (run LintRun) Report() *File {
	cmd := []string{
		"golangci-lint", "run",
		"-v",
		"--timeout", "5m",
		// Disable limits, we can filter the report instead
		"--max-issues-per-linter", "0",
		"--max-same-issues", "0",
		"--out-format", "json",
		"--issues-exit-code", "0",
	}
	return dag.
		Container().
		From(lintImage).
		WithFile("/etc/golangci.yml", dag.CurrentModule().Source().File("lint-config.yml")).
		WithEnvVariable("GOLANGCI_LINT_CONFIG", "/etc/golangci.yml").
		WithMountedDirectory("/src", run.Source).
		WithWorkdir(path.Join("/src", run.Path)).
		// Uncomment to debug:
		// WithEnvVariable("DEBUG_CMD", strings.Join(cmd, " ")).
		// Terminal().
		WithExec(cmd, ContainerWithExecOpts{RedirectStdout: "golangci-lint-report.json"}).
		File("golangci-lint-report.json")
}

type Replacement struct {
	Text string `json:"Text"`
}

type Position struct {
	Filename string `json:"Filename"`
	Offset   int    `json:"Offset"`
	Line     int    `json:"Line"`
	Column   int    `json:"Column"`
}

type Issue struct {
	Text           string       `json:"Text"`
	FromLinter     string       `json:"FromLinter"`
	SourceLines    []string     `json:"SourceLines"`
	Replacement    *Replacement `json:"Replacement,omitempty"`
	Pos            Position     `json:"Pos"`
	ExpectedNoLint bool         `json:"ExpectedNoLint"`
	Severity       string       `json:"Severity"`
}

func (issue Issue) Summary() string {
	return fmt.Sprintf("%s:%d %s: %s",
		issue.Pos.Filename,
		issue.Pos.Line,
		issue.Severity,
		issue.Text,
	)
}

// Low-level report schema
// We don't expose this type directly, for flexibility to:
// 1) mix lazy and non-lazy functions
// 2) augment the schema with "smart' functions
type reportSchema struct {
	Issues []Issue `json:"Issues"`
}

func (run LintRun) parseReport(ctx context.Context) (*reportSchema, error) {
	reportJSON, err := run.Report().Contents(ctx)
	if err != nil {
		return nil, err
	}
	var report reportSchema
	return &report, json.Unmarshal([]byte(reportJSON), &report)
}
