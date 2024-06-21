package main

import (
	"context"
	"encoding/json"

	"github.com/dagger/dagger/ci/internal/dagger"
)

// A typescript linter
type TypescriptLint struct {
	Cfg *File // +private
}

func (tl TypescriptLint) WithConfig(config *File) TypescriptLint {
	tl.Cfg = config
	return tl
}

// Lint a typescript codebae
func (tl TypescriptLint) Lint(
	ctx context.Context,
	// The source directory to lint
	source *Directory,
) (*LintReport, error) {
	var (
		eslintPkg = "https://registry.yarnpkg.com/eslint/-/eslint-9.5.0.tgz#11856034b94a9e1a02cfcc7e96a9f0956963cd2f"
		config    = tl.Cfg
	)
	if config == nil {
		config = source.File("eslint.config.js")
	}
	llreportFile := dag.
		Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{"nodejs", "yarn"},
		}).
		WithFile("eslint.config.js", config).
		WithMountedDirectory("/src", source).
		WithWorkdir("/src").
		WithExec([]string{"yarn", "add", eslintPkg}).
		WithExec([]string{
			"sh", "-c",
			"yarn eslint --max-warnings=0 -f json -o eslint-report.json . || true",
		}).
		File("eslint-report.json")
	llreportJSON, err := llreportFile.Contents(ctx)
	if err != nil {
		return nil, err
	}
	// Unmarshal the low-level report from linting tool
	var llreport []struct {
		FilePath string `json:"filePath"`
		Messages []struct {
			RuleID    string `json:"ruleId"`
			Severity  int    `json:"severity"`
			Message   string `json:"message"`
			Line      int    `json:"line"`
			Column    int    `json:"column"`
			NodeType  string `json:"nodeType"`
			MessageID string `json:"messageId"`
			EndLine   int    `json:"endLine"`
			EndColumn int    `json:"endColumn"`
		} `json:"messages"`
		ErrorCount          int    `json:"errorCount"`
		WarningCount        int    `json:"warningCount"`
		FixableErrorCount   int    `json:"fixableErrorCount"`
		FixableWarningCount int    `json:"fixableWarningCount"`
		Source              string `json:"source"`
	}
	if err := json.Unmarshal([]byte(llreportJSON), &llreport); err != nil {
		return nil, err
	}
	report := new(LintReport)
	report.LLReport = llreportFile // Keep the low-level report just in case
	for _, llissue := range llreport {
		for _, message := range llissue.Messages {
			report.Issues = append(report.Issues, LintIssue{
				Text:    message.Message,
				IsError: true, // FIXME: parse severity
				Tool:    "eslint",
			})
		}
	}
	return report, nil
}
