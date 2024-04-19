package linter

import (
	"fmt"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

type LinterRule[F any] struct {
	Name        string
	Description string
	URL         string
	Format      F
}

func (rule LinterRule[F]) Run(warn LintWarnFunc, location []parser.Range, txt ...string) {
	startLine := 0
	if len(location) > 0 {
		startLine = location[0].Start.Line
	}
	if len(txt) == 0 {
		txt = []string{rule.Description}
	}
	short := strings.Join(txt, " ")
	short = fmt.Sprintf("Lint Rule '%s': %s (line %d)", rule.Name, short, startLine)
	warn(short, rule.URL, [][]byte{[]byte(rule.Description)}, location)
}

type LintWarnFunc func(short, url string, detail [][]byte, location []parser.Range)
