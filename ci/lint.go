package main

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	golangCiLintImage = "docker.io/golangci/golangci-lint@sha256:b5f8712114561f1e2fbe74d04ed07ddfd992768705033a6251f3c7b848eac38e"
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

// A Python linter
type PythonLint struct{}

func (pl PythonLint) Lint(
	ctx context.Context,
	// The Python source directory to lint
	source *Directory,
	pythonVersion string
) (*LintReport, error) {
	version := "3.11"
	base := dag.Container().
		From(fmt.Sprintf(
			"docker.io/library/python:3.11-slim@sha256:fc39d2e68b554c3f0a5cb8a776280c0b3d73b4c04b83dbade835e2a171ca27ef",
			version,
		).
		WithEnvVariable("PIPX_BIN_DIR", "/usr/local/bin").
		WithMountedCache("/root/.cache/pip", dag.CacheVolume("pip_cache_"+version)).
		WithMountedCache("/root/.local/pipx/cache", dag.CacheVolume("pipx_cache_"+version)).
		WithMountedCache("/root/.cache/hatch", dag.CacheVolume("hatch_cache_"+version)).
		WithMountedFile("/pipx.pyz", pipx).
		WithExec([]string{"python", "/pipx.pyz", "install", "hatch==1.7.0"}).
		WithExec([]string{"python", "-m", "venv", venv}).
		WithEnvVariable("VIRTUAL_ENV", venv).
		WithEnvVariable(
			"PATH",
			"$VIRTUAL_ENV/bin:$PATH",
			dagger.ContainerWithEnvVariableOpts{
				Expand: true,
			},
		).
		WithEnvVariable("HATCH_ENV_TYPE_VIRTUAL_PATH", venv).
		// Mirror the same dir structure from the repo because of the
		// relative paths in ruff (for docs linting).
		WithWorkdir(pythonSubdir).
		WithMountedFile("requirements.txt", src.File("requirements.txt")).
		WithExec([]string{"pip", "install", "-r", "requirements.txt"})

	WithExec([]string{"ruff", "check", "--show-source", ".", "/docs"}).

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

func (lr *LintReport) Merge(others []LintReport) LintReport {
	// flatten the issues into a single LintReport object
	var issues []LintIssue
	for _, other := range others {
		issues = append(issues, other.Issues...)
	}
	return LintReport{
		Issues: issues,
	}
}

func (lr *LintReport) WithIssue(text string, isError bool) LintReport {
	return LintReport{
		Issues: append(lr.Issues, LintIssue{
			IsError: isError,
			Text:    text,
		}),
	}
}
