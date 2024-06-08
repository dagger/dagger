package main

import (
	"context"
	"encoding/json"
	"fmt"
)

func New(
	// Project source directory
	source *Directory,
	// Go version
	// +optional
	// +default="1.22.4"
	version string,
	// Go linter version
	// +optional
	// +default="1.59"
	lintVersion string,
) *Go {
	if source == nil {
		source = dag.Directory()
	}
	return &Go{
		Version:     version,
		LintVersion: lintVersion,
		Source:      source,
	}
}

// A Go project
type Go struct {
	// Go version
	Version string
	// Go linter version
	LintVersion string

	// Project source directory
	Source *Directory
}

// Build a base container with Go installed and configured
func (p *Go) Base() *Container {
	return dag.
		Wolfi().
		Container(WolfiContainerOpts{Packages: []string{
			"go~" + p.Version,
			// gcc is needed to run go test -race https://github.com/golang/go/issues/9918 (???)
			"build-base",
			// adding the git CLI to inject vcs info
			// into the go binaries
			"git",
		}}).
		WithEnvVariable("GOLANG_VERSION", p.Version).
		WithEnvVariable("GOPATH", "/go").
		WithEnvVariable("PATH", "${GOPATH}/bin:${PATH}", ContainerWithEnvVariableOpts{Expand: true}).
		WithDirectory("/usr/local/bin", dag.Directory()).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("go-mod")).
		// include a cache for go build
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("go-build"))
}

// Prepare a build environment for the given Go source code
//   - Build a base container with Go tooling installed and configured
//   - Mount the source code
//   - Download dependencies
func (p *Go) Env() *Container {
	return p.
		Base().
		WithEnvVariable("CGO_ENABLED", "0").
		WithWorkdir("/app").
		// run `go mod download` with only go.mod files (re-run only if mod files have changed)
		WithDirectory("/app", p.Source, ContainerWithDirectoryOpts{
			Include: []string{"**/go.mod", "**/go.sum"},
		}).
		WithExec([]string{"go", "mod", "download"}).
		// run `go build` with all source
		WithMountedDirectory("/app", p.Source)
}

// Lint the Go code
func (p *Go) Lint(ctx context.Context) (*LintReport, error) {
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
		From(fmt.Sprintf("golangci/golangci-lint:v%s-alpine", p.LintVersion)).
		WithFile("/etc/golangci.yml", config).
		WithEnvVariable("GOLANGCI_LINT_CONFIG", "/etc/golangci.yml").
		WithMountedDirectory("/src", p.Source).
		WithWorkdir("/src").
		WithExec(cmd, ContainerWithExecOpts{RedirectStdout: "lint.json"}).
		File("lint.json")
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
	}
	return report, nil
}

// A linting report
type LintReport struct {
	Issues   []LintIssue
	LLReport *File // +private
}

// An individual linting issue
type LintIssue struct {
	IsError bool
	Text    string
	// FIXME add more fields
}

// Return the linting report as a JSON file
func (li LintReport) JSON(
	ctx context.Context,
	// Return the low-level linting tool's report instead of the high-level one
	// Note: currently this is golangci-lint's format, but could change in the future
	// +optional
	ll bool,
) (*File, error) {
	if ll {
		return li.LLReport, nil
	}
	data, err := json.MarshalIndent(li, "", "  ")
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
func (li LintReport) ErrorCount() int {
	var count int
	for _, issue := range li.Issues {
		if issue.IsError {
			count += 1
		}
	}
	return count
}

// Return the total number of linting warnings
func (li LintReport) WarningCount() int {
	var count int
	for _, issue := range li.Issues {
		if !issue.IsError {
			count += 1
		}
	}
	return count
}

// Return the total number of linting issues (errors and warnings)
func (li LintReport) IssueCount() int {
	return len(li.Issues)
}

// Lint the Go project, and return an error if any check fails,
// without returning any additional details.
func (p *Go) AssertLintPass(ctx context.Context) error {
	report, err := p.Lint(ctx)
	if err != nil {
		return err
	}
	if report.ErrorCount() > 0 {
		return fmt.Errorf("linting failed with %d errors", report.ErrorCount())
	}
	return nil
}
