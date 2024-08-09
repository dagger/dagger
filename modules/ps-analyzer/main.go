// The PowerShell Script Analyzer (PSScriptAnalyzer) for Dagger.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/modules/ps-analyzer/internal/dagger"
)

const psScriptAnalyzerVersion = "1.22.0"

// The PowerShell PSScriptAnalyzer severity.
type Severity int

const (
	Information = iota
	Warning
	Error
	ParseError
)

func (s Severity) String() string {
	switch s {
	case Information:
		return "Information"
	case Warning:
		return "Warning"
	case Error:
		return "Error"
	case ParseError:
		return "ParseError"
	default:
		panic("it should be reach")
	}
}

type PsAnalyzer struct{}

func (m *PsAnalyzer) Check(ctx context.Context,
	// The file that want to check.
	file *dagger.File,
	// Exclude rules out of the analyzer.
	//
	// +optional
	excludeRules []string,
) (*Report, error) {
	filename, err := file.Name(ctx)
	if err != nil {
		return nil, err
	}

	excludeRuleArg := ""
	if len(excludeRules) != 0 {
		excludeRuleArg = "-ExcludeRule " + toPSArraySubExpr(excludeRules)
	}

	out, err := m.Base().
		WithMountedFile("/"+filename, file).
		WithExec([]string{"pwsh", "-c", fmt.Sprintf("Invoke-ScriptAnalyzer -Path /%s %s | ConvertTo-Json -Depth 1 -WarningAction Ignore", filename, excludeRuleArg)}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Target: file,
		JSON:   out,
	}

	if len(out) == 0 {
		return report, nil
	}

	var issues []struct {
		Message  string
		RuleName string
		Severity Severity
		Line     int
		Column   int
	}

	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, err
	}

	for _, iss := range issues {
		issue := Issue{
			Filename: filename,
			Line:     iss.Line,
			Column:   iss.Column,
			Message:  iss.Message,
			Level:    iss.Severity.String(),
		}

		report.Issues = append(report.Issues, issue)
	}

	return report, nil
}

func (m *PsAnalyzer) Base() *dagger.Container {
	analyzerPackage := dag.HTTP(
		fmt.Sprintf("https://github.com/PowerShell/PSScriptAnalyzer/releases/download/%s/PSScriptAnalyzer.%s.nupkg", psScriptAnalyzerVersion, psScriptAnalyzerVersion),
	)
	modulesPath := "/root/.local/share/powershell/Modules"

	return dag.Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{
				"powershell",
			},
		}).
		WithMountedFile("/PSScriptAnalyzer.nupkg", analyzerPackage).
		WithExec([]string{"mkdir", "-p", modulesPath}).
		WithExec([]string{"unzip", "/PSScriptAnalyzer.nupkg", "-d", modulesPath + "/PSScriptAnalyzer"})
}

type Report struct {
	Target *dagger.File // +private

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

func toPSArraySubExpr(elems []string) string {
	arr := make([]string, len(elems))
	for i, elem := range elems {
		arr[i] = fmt.Sprintf(`"%s"`, elem)
	}
	return fmt.Sprintf("@(%s)", strings.Join(arr, ","))
}
