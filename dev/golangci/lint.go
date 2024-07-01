package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
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
	// Lint specific packages within the source directory
	// +optional
	packages []string,
) LintRun {
	return LintRun{Source: source, Packages: packages}
}

// The result of running the GolangCI lint tool
type LintRun struct {
	// +private
	Source *Directory
	// +private
	Packages []string
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
	if run.Packages != nil {
		cmd = append(cmd, run.Packages...)
	}
	return dag.
		Container().
		From(lintImage).
		WithFile("/etc/golangci.yml", dag.CurrentModule().Source().File("lint-config.yml")).
		WithEnvVariable("GOLANGCI_LINT_CONFIG", "/etc/golangci.yml").
		WithMountedDirectory("/src", run.Source).
		WithWorkdir("/src").
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

func (run LintRun) Issues(ctx context.Context) ([]Issue, error) {
	report, err := run.parseReport(ctx)
	if err != nil {
		return nil, err
	}
	return report.Issues, nil
}

func (run LintRun) ErrorCount(ctx context.Context) (int, error) {
	var count int
	issues, err := run.Issues(ctx)
	if err != nil {
		return count, err
	}
	for _, issue := range issues {
		if issue.Severity == "error" {
			count += 1
		}
	}
	return count, nil
}

func (run LintRun) WarningCount(ctx context.Context) (int, error) {
	var count int
	issues, err := run.Issues(ctx)
	if err != nil {
		return count, err
	}
	for _, issue := range issues {
		if issue.Severity != "error" {
			count += 1
		}
	}
	return count, nil
}

// A linting report
type LintReport struct {
	mu     sync.Mutex
	Issues []LintIssue
	// +private
	LLReport *File `json:"-"`
}

// An individual linting issue
type LintIssue struct {
	// The name of the tool that produced the issue
	Tool string
	// True if the issue is an error, false if it's a warning'
	IsError bool
	// The text explaining the issue
	Text string
	// FIXME add more fields
}

// Return the linting report as a JSON file
func (lr *LintReport) JSON(
	ctx context.Context,
	// Return the low-level linting tool's report instead of the high-level one
	// +optional
	ll bool,
) (*File, error) {
	if ll {
		if lr.LLReport == nil {
			return nil, fmt.Errorf("no low-level report available")
		}
		return lr.LLReport, nil
	}
	data, err := json.MarshalIndent(lr, "", "  ")
	if err != nil {
		return nil, err
	}
	f := dag.
		Directory().
		WithNewFile("lint.json", string(data)).
		File("lint.json")
	return f, nil
}

// Return the total number of linting errors
func (lr *LintReport) ErrorCount() int {
	var count int
	for _, issue := range lr.Issues {
		if issue.IsError {
			count += 1
		}
	}
	return count
}

// Return the total number of linting warnings
func (lr *LintReport) WarningCount() int {
	var count int
	for _, issue := range lr.Issues {
		if !issue.IsError {
			count += 1
		}
	}
	return count
}

// Return the total number of linting issues (errors and warnings)
func (lr *LintReport) IssueCount() int {
	return len(lr.Issues)
}

// Return an error if there are errors
func (lr *LintReport) AssertPass(ctx context.Context) error {
	if count := lr.ErrorCount(); count > 0 {
		return fmt.Errorf("linting failed with %d errors", count)
	}
	return nil
}

func (lr *LintReport) merge(other *LintReport) error {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lr.Issues = append(lr.Issues, other.Issues...)
	return nil
}

func (lr *LintReport) WithIssue(text string, isError bool) *LintReport {
	return &LintReport{
		Issues: append(lr.Issues, LintIssue{
			IsError: isError,
			Text:    text,
		}),
	}
}
