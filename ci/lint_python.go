package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// A Python linter
type PythonLint struct{}

func (pl PythonLint) Lint(
	ctx context.Context,
	// The Python source directory to lint
	source *Directory,
) (*LintReport, error) {
	var (
		version    = "3.11"
		baseDigest = "sha256:fc39d2e68b554c3f0a5cb8a776280c0b3d73b4c04b83dbade835e2a171ca27ef"
		cmd        = []string{
			"ruff", "check",
			"--exit-zero",
			"--output-format", "json",
			".",
		}
	)
	llreportFile := dag.
		Container().
		From(fmt.Sprintf("docker.io/library/python:%s-slim@%s", version, baseDigest)).
		WithExec([]string{"pip", "install", "ruff==0.4.9"}).
		WithMountedDirectory("/src", source).
		WithWorkdir("/src").
		//		WithEnvVariable("PIPX_BIN_DIR", "/usr/local/bin").
		//		WithMountedCache("/root/.cache/pip", dag.CacheVolume("pip_cache_"+version)).
		//		WithMountedCache("/root/.local/pipx/cache", dag.CacheVolume("pipx_cache_"+version)).
		//		WithMountedCache("/root/.cache/hatch", dag.CacheVolume("hatch_cache_"+version)).
		//		WithMountedFile("/pipx.pyz", dag.HTTP("https://github.com/pypa/pipx/releases/download/1.2.0/pipx.pyz")).
		//		WithExec([]string{"python", "/pipx.pyz", "install", "hatch==1.7.0"}).
		//		WithExec([]string{"python", "-m", "venv", "/opt/venv"}).
		//		WithEnvVariable("VIRTUAL_ENV", "/opt/venv").
		//		WithEnvVariable(
		//			"PATH",
		//			"$VIRTUAL_ENV/bin:$PATH",
		//			dagger.ContainerWithEnvVariableOpts{Expand: true},
		//		).
		//		WithEnvVariable("HATCH_ENV_TYPE_VIRTUAL_PATH", "/opt/venv").
		//		WithMountedFile("requirements.txt", source.File("requirements.txt")).
		//		WithExec([]string{"pip", "install", "-r", "requirements.txt"}).
		WithExec(cmd, ContainerWithExecOpts{RedirectStdout: "ruff-report.json"}).
		File("ruff-report.json")
	llreportJSON, err := llreportFile.Contents(ctx)
	if err != nil {
		return nil, err
	}
	var llreport []struct {
		Cell        *string `json:"cell"`
		Code        string  `json:"code"`
		EndLocation struct {
			Column int `json:"column"`
			Row    int `json:"row"`
		} `json:"end_location"`
		Filename string `json:"filename"`
		Fix      struct {
			Applicability string `json:"applicability"`
			Edits         []struct {
				Content     string `json:"content"`
				EndLocation struct {
					Column int `json:"column"`
					Row    int `json:"row"`
				} `json:"end_location"`
				Location struct {
					Column int `json:"column"`
					Row    int `json:"row"`
				} `json:"location"`
			} `json:"edits"`
			Message string `json:"message"`
		} `json:"fix"`
		Location struct {
			Column int `json:"column"`
			Row    int `json:"row"`
		} `json:"location"`
		Message string `json:"message"`
		NoqaRow int    `json:"noqa_row"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal([]byte(llreportJSON), &llreport); err != nil {
		return nil, err
	}
	report := &LintReport{
		Issues:   []LintIssue{},
		LLReport: llreportFile, // Keep the low-level report just in case
	}
	for _, llissue := range llreport {
		report.Issues = append(report.Issues, LintIssue{
			Text:    llissue.Message,
			IsError: true, // No concept of error/warning
			Tool:    "ruff",
		})
	}
	return report, nil
}
