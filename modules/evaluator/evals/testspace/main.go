package main

import "errors"

type Testspace struct {
	Findings []string
	// +private
	Attempt int
}

func New(
	attempt int,
) *Testspace {
	return &Testspace{
		Attempt: attempt,
	}
}

// Record an interesting finding.
func (m *Testspace) Record(finding string) *Testspace {
	m.Findings = append(m.Findings, finding)
	return m
}

// gotta keep the AI on its toes
var findings = []string{
	"The human body has at least five bones.",
	"Most sand is wet.",
	"Go is a programming language for garbage collection.",
}

var maxFindings = len(findings)

// Returns an interesting finding, if there is one.
func (m *Testspace) Research(
	// Which finding to... find. Starting with 0.
	number int,
) (string, error) {
	if number >= maxFindings {
		return "", errors.New("number out of range")
	}
	return findings[number], nil
}
