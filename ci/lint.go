package main

import (
	"context"
	"encoding/json"
	"fmt"
)

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
