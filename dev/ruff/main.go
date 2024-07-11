package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dev/ruff/internal/dagger"
)

var (
	pythonVersion     = "3.11"
	pythonImageRepo   = "docker.io/library/python"
	pythonImageTag    = fmt.Sprintf("%s-slim", pythonVersion)
	pythonImageDigest = "sha256:fc39d2e68b554c3f0a5cb8a776280c0b3d73b4c04b83dbade835e2a171ca27ef"
	pythonImage       = pythonImageRepo + ":" + pythonImageTag + "@" + pythonImageDigest
)

// Ruff is a fast Python linter implemented in Rust
type Ruff struct{}

// Lint a Python codebase
func (ruff Ruff) Lint(
	ctx context.Context,
	// The Python source directory to lint
	source *dagger.Directory,
) *LintRun {
	return &LintRun{
		Source: source,
	}
}

// The result of running the Ruff lint tool
type LintRun struct {
	// +private
	Source *dagger.Directory
}

// Return a JSON report file for this run
func (run LintRun) Report() *dagger.File {
	cmd := []string{
		"ruff", "check",
		"--exit-zero",
		"--output-format", "json",
		".",
	}
	return dag.
		Container().
		From(pythonImage).
		WithExec([]string{"pip", "install", "ruff==0.4.9"}).
		WithMountedDirectory("/src", run.Source).
		WithWorkdir("/src").
		WithExec(cmd, dagger.ContainerWithExecOpts{RedirectStdout: "ruff-report.json"}).
		File("ruff-report.json")
}

// Return a list of issues produced by the lint run
func (run LintRun) Issues(ctx context.Context) ([]Issue, error) {
	report, err := run.parseReport(ctx)
	if err != nil {
		return nil, err
	}
	return *report, nil
}

// Return an error if the lint run reported issues of severity "error"
func (run LintRun) Assert(ctx context.Context) error {
	errCount, err := run.ErrorCount(ctx)
	if err != nil {
		return err
	}
	if errCount > 0 {
		summary, err := run.Summary(ctx)
		if err != nil {
			return err
		}
		return errors.New(summary)
	}
	return nil
}

// Return a text summary of the lint run
func (run LintRun) Summary(ctx context.Context) (string, error) {
	var (
		count    int
		errCount int
		lines    []string
	)
	issues, err := run.Issues(ctx)
	if err != nil {
		return "", err
	}
	for _, iss := range issues {
		if iss.IsError() {
			errCount += 1
		}
		count += 1
		lines = append(lines, " - "+iss.Summary())
	}
	return fmt.Sprintf("%d linting issues (%d errors)\n%s", count, errCount, strings.Join(lines, "\n")), nil
}

// Return the number of linting errors reported by this run
func (run LintRun) ErrorCount(ctx context.Context) (int, error) {
	// Note: keep the portable implementation
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

// Return the number of non-error linting issues reported by this run
func (run LintRun) WarningCount(ctx context.Context) (int, error) {
	// Note: keep the portable implementation (this will always return zero)
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

// Return true if this linting issue is considered an error
// Note: this always returns true, because ruff doesn't have a concept of issue severity
func (issue Issue) IsError() bool {
	return true
}

func (run LintRun) parseReport(ctx context.Context) (*reportSchema, error) {
	reportJSON, err := run.Report().Contents(ctx)
	if err != nil {
		return nil, err
	}
	var report reportSchema
	return &report, json.Unmarshal([]byte(reportJSON), &report)
}

// A Ruff lint report
type reportSchema []Issue

// An individual issue in a Ruff lint report
type Issue struct {
	Cell        *string  `json:"cell"`
	Code        string   `json:"code"`
	EndLocation Location `json:"end_location"`
	AbsFilename string   `json:"filename"` // +private
	Fix         Fix      `json:"fix"`
	Location    Location `json:"location"`
	Message     string   `json:"message"`
	NoqaRow     int      `json:"noqa_row"`
	URL         string   `json:"url"`
}

func (issue Issue) Filename() string {
	// Ruff uses absolute filename in its json reports,
	// so we remove the /src mountpoint in the container
	return strings.TrimPrefix(issue.AbsFilename, "/src/")
}

func (issue Issue) Summary() string {
	return fmt.Sprintf("%s:%d %s: %s",
		issue.Filename(),
		issue.Location.Row,
		"error",
		issue.Message,
	)
}

type Location struct {
	Column int `json:"column"`
	Row    int `json:"row"`
}

type Fix struct {
	Applicability string `json:"applicability"`
	Edits         []Edit `json:"edits"`
	Message       string `json:"message"`
}

type Edit struct {
	Content     string   `json:"content"`
	EndLocation Location `json:"end_location"`
	Location    Location `json:"location"`
}
