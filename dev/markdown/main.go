// The markdown linter used to develop Dagger

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"text/template"
)

type Markdown struct{}

// Return the markdown linting rules file
func (m *Markdown) Rules() *File {
	return dag.
		CurrentModule().
		Source().
		File("markdownlint.yaml")
}

// Build a container with a markdown linter tool installed
func (m *Markdown) Container(
	// Source directory to lint
	source *Directory,
) *Container {
	return dag.
		Container().
		From("tmknom/markdownlint:0.31.1").
		WithoutEntrypoint().
		WithUser("0").
		WithMountedFile("/etc/markdownlint.yaml", m.Rules()).
		WithWorkdir("/src").
		WithDirectory("/src", source)
}

// Fix simple markdown errors
func (m *Markdown) Fix(source *Directory) *Directory {
	return m.
		Container(source).
		WithExec([]string{
			"markdownlint", "-c", "/etc/markdownlint.yaml", "--fix", "--", ".",
		}).
		Directory(".")
}

// Check a source directory for linter errors
func (m *Markdown) Lint(
	ctx context.Context,
	// Source directory to lint
	source *Directory,
) (*Report, error) {
	raw, err := m.
		Container(source).
		WithExec([]string{
			"sh", "-c", "markdownlint -c /etc/markdownlint.yaml --json -- . 2>&1 || true",
		}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}
	return &Report{
		JSON:   raw,
		Source: source,
	}, nil
}

type Report struct {
	JSON string
	// +private
	Source *Directory
}

func (r *Report) Checks(
	// Only return the first N checks
	// +optional
	// +default=0
	limit int,
) ([]*Check, error) {
	var checks []*Check
	if err := json.Unmarshal([]byte(r.JSON), &checks); err != nil {
		return nil, err
	}
	if limit > len(checks) {
		limit = len(checks)
	}
	if limit > 0 {
		checks = checks[:limit]
	}
	for i := range checks {
		checks[i].Report = r
	}
	return checks, nil
}

func (r *Report) Files() (*Directory, error) {
	checks, err := r.Checks(0)
	if err != nil {
		return nil, err
	}
	dir := dag.Directory()
	for _, check := range checks {
		if check.FileName == "" {
			continue
		}
		dir = dir.WithFile(check.FileName, r.Source.File(check.FileName))
	}
	return dir, nil
}

// A single lint check
type Check struct {
	// +private
	Report          *Report
	FileName        string   `json:"fileName"`
	LineNumber      int      `json:"lineNumber"`
	RuleNames       []string `json:"ruleNames"`
	RuleDescription string   `json:"ruleDescription"`
	RuleInformation string   `json:"ruleInformation"`
	ErrorDetail     string   `json:"errorDetail"`
	ErrorContext    string   `json:"errorContext"`
	// ErrorRange      *string  `json:"errorRange"` // Assuming errorRange is a string or null
	// FixInfo         struct {
	// 	InsertText string `json:"insertText"`
	// } `json:"fixInfo"`
}

// JSON-encoded check
func (c *Check) JSON() (string, error) {
	b, err := json.Marshal(c)
	return string(b), err
}

func (c *Check) File() *File {
	if c.FileName == "" {
		return nil
	}
	return c.Report.Source.File(c.FileName)
}

func (c *Check) Fix() *File {
	if c.FileName == "" {
		return nil
	}
	return new(Markdown).Fix(c.Report.Source).File(c.FileName)
}

func (c *Check) Diff(ctx context.Context) (string, error) {
	return dag.
		Wolfi().
		Container().
		WithMountedFile("orig", c.File()).
		WithMountedFile("fixed", c.Fix()).
		WithExec([]string{"sh", "-c", "diff -u orig fixed || true"}).
		Stdout(ctx)
}

func (c *Check) Format(text string) (string, error) {
	tmpl, err := template.New("markdown.lint.check.format").Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, c)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
