package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/dagger/dagger/modules/golangci/internal/dagger"
)

const (
	lintImageRepo   = "docker.io/golangci/golangci-lint"
	lintImageTag    = "v1.60-alpine"
	lintImageDigest = "sha256:e71ee0fd4db9214586a95cbdd0237bcb2f2b4ddfdf55805dfeb3bcf6cbab3333"
	lintImage       = lintImageRepo + ":" + lintImageTag + "@" + lintImageDigest
)

// Lint a go codebase
func (gl Golangci) Lint(
	// The Go source directory to lint
	source *dagger.Directory,
	// Lint a specific path within the source directory
	// +optional
	path string,
	// A cache volume to use for go module downloads
	// +optional
	goModCache *dagger.CacheVolume,
	// A cache volume to use for go build
	// +optional
	goBuildCache *dagger.CacheVolume,
) LintRun {
	return LintRun{
		Source:       source,
		Path:         path,
		GoModCache:   goModCache,
		GoBuildCache: goBuildCache,
	}
}

// The result of running the GolangCI lint tool
type LintRun struct {
	// +private
	Source *dagger.Directory
	// +private
	Path string
	// +private
	GoModCache *dagger.CacheVolume
	// +private
	GoBuildCache *dagger.CacheVolume
}

func (run LintRun) Issues(ctx context.Context) ([]*Issue, error) {
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
		if !iss.IsError() {
			continue
		}
		errCount += 1
		summaries = append(summaries, iss.Summary())
	}
	if errCount > 0 {
		return fmt.Errorf("linting failed with %d issues:\n%s",
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
	return issue.Severity == "error"
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
func (run LintRun) Report() *dagger.File {
	home := "/root"
	cmd := []string{
		"golangci-lint", "run",
		"-v",
		"--timeout", "10m",
		// Disable limits, we can filter the report instead
		"--max-issues-per-linter", "0",
		"--max-same-issues", "0",
		"--out-format", "json",
		"--issues-exit-code", "0",
		"--config", path.Join(home, ".golangci.yml"),
	}

	goModCache := run.GoModCache
	if goModCache == nil {
		goModCache = dag.CacheVolume("go-mod")
	}
	goBuildCache := run.GoBuildCache
	if goBuildCache == nil {
		goBuildCache = dag.CacheVolume("go-build")
	}

	return dag.
		Container().
		From(lintImage).
		// FIXME should be "${HOME}/.golangci.yml"
		WithFile(path.Join(home, ".golangci.yml"), dag.CurrentModule().Source().File("lint-config.yml"), dagger.ContainerWithFileOpts{}).
		WithMountedDirectory("/src", run.Source).
		WithMountedCache("/go/pkg/mod", goModCache).
		WithMountedCache("/root/.cache/go-build", goBuildCache).
		WithWorkdir(path.Join("/src", run.Path)).
		// Uncomment to debug:
		// WithEnvVariable("DEBUG_CMD", strings.Join(cmd, " ")).
		// Terminal().
		WithExec(cmd, dagger.ContainerWithExecOpts{
			RedirectStdout: "golangci-lint-report.json",
		}).
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
	Text           string      `json:"Text"`
	FromLinter     string      `json:"FromLinter"`
	SourceLines    []string    `json:"SourceLines"`
	Replacement    Replacement `json:"Replacement,omitempty"`
	Pos            Position    `json:"Pos"`
	ExpectedNoLint bool        `json:"ExpectedNoLint"`
	Severity       string      `json:"Severity"`
}

func (issue Issue) Summary() string {
	return fmt.Sprintf("[%s] %s:%d: %s",
		issue.FromLinter,
		issue.Pos.Filename,
		issue.Pos.Line,
		issue.Text,
	)
}

// Low-level report schema
// We don't expose this type directly, for flexibility to:
// 1) mix lazy and non-lazy functions
// 2) augment the schema with "smart' functions
type reportSchema struct {
	Issues []*Issue `json:"Issues"`
}

func (run LintRun) parseReport(ctx context.Context) (*reportSchema, error) {
	reportJSON, err := run.Report().Contents(ctx)
	if err != nil {
		return nil, err
	}
	var report reportSchema
	if err := json.Unmarshal([]byte(reportJSON), &report); err != nil {
		return nil, err
	}
	for _, issue := range report.Issues {
		// get the full path
		issue.Pos.Filename = path.Join(run.Path, issue.Pos.Filename)
		// normalize the severity
		if issue.Severity == "" {
			issue.Severity = "error"
		}
	}
	return &report, nil
}
