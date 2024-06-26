package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/ci/std/shellcheck/internal/dagger"
)

type Shellcheck struct{}

// Returns a container that echoes whatever string argument is provided
func (m *Shellcheck) Check(ctx context.Context, file *File) (*Report, error) {
	filename, err := file.Name(ctx)
	if err != nil {
		return nil, err
	}
	filename = filepath.Join(".", filename)

	base := base().
		WithWorkdir("/src").
		WithMountedFile(filename, file)

	// TODO: don't run shellcheck multiple times, collect multiple outputs

	reportRaw, err := base.WithExec(shellcheck(filename, "")).Stdout(ctx)
	if err != nil {
		return nil, err
	}

	issuesRaw, err := base.WithExec(shellcheck(filename, "json1")).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	var issues struct {
		Comments []struct {
			Filename  string `json:"file"`
			Line      int    `json:"line"`
			EndLine   int    `json:"endLine"`
			Column    int    `json:"column"`
			EndColumn int    `json:"endColumn"`

			Level   string `json:"level"`
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"comments"`
	}
	err = json.Unmarshal([]byte(issuesRaw), &issues)
	if err != nil {
		return nil, err
	}

	fixDiffRaw, err := base.WithExec(shellcheck(filename, "diff")).Stdout(ctx)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Target:    file,
		JSON:      issuesRaw,
		Report:    reportRaw,
		FixedDiff: fixDiffRaw,
	}
	for _, comment := range issues.Comments {
		issue := Issue{
			Filename:  comment.Filename,
			Line:      comment.Line,
			LineEnd:   comment.EndLine,
			Column:    comment.Column,
			ColumnEnd: comment.EndColumn,
			Level:     comment.Level,
			Code:      comment.Code,
			Message:   comment.Message,
		}
		report.Issues = append(report.Issues, issue)
	}
	return report, nil
}

type Report struct {
	Target *File // +private

	Issues    []Issue
	JSON      string
	Report    string
	FixedDiff string
}

type Issue struct {
	Filename  string
	Line      int
	LineEnd   int
	Column    int
	ColumnEnd int

	Level   string
	Code    int
	Message string
}

func (r *Report) Assert() error {
	if len(r.Issues) > 0 {
		return fmt.Errorf("linting failed with %d issues:\n%s", len(r.Issues), r.Report)
	}
	return nil
}

func (r *Report) Fixed(ctx context.Context) (*File, error) {
	filename, err := r.Target.Name(ctx)
	if err != nil {
		return nil, err
	}
	filename = filepath.Join(".", filename)

	f := base().
		WithExec([]string{"apk", "add", "patch"}).
		WithWorkdir("/src").
		WithNewFile(filename+".patch", dagger.ContainerWithNewFileOpts{
			Contents: r.FixedDiff,
		}).
		WithFile(filename, r.Target).
		WithExec([]string{"patch", filename, filename + ".patch"}).
		File(filename)
	return f, nil
}

func shellcheck(filename string, format string) []string {
	if format == "" {
		format = "tty"
	}

	cmd := fmt.Sprintf("shellcheck %q --format=%q --exclude=SC2317", filename, format)

	return []string{
		"sh",
		"-c",
		cmd + "; code=$?; if [ $code -eq 1 ]; then code=0; fi; exit $code",
	}
}

func base() *Container {
	return dag.Container().
		From("koalaman/shellcheck-alpine:v0.10.0").
		WithoutEntrypoint()
}
