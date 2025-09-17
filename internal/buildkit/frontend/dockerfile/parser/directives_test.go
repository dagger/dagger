package parser

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirectives(t *testing.T) {
	t.Parallel()

	dt := `#escape=\
# key = FOO bar

# smth
`

	parser := DirectiveParser{}
	d, err := parser.ParseAll([]byte(dt))
	require.NoError(t, err)
	require.Len(t, d, 1)

	require.Equal(t, "escape", d[0].Name)
	require.Equal(t, "\\", d[0].Value)

	// for some reason Moby implementation in case insensitive for escape
	dt = `# EScape=\
# KEY = FOO bar

# smth
`

	parser = DirectiveParser{}
	d, err = parser.ParseAll([]byte(dt))
	require.NoError(t, err)
	require.Len(t, d, 1)

	require.Equal(t, "escape", d[0].Name)
	require.Equal(t, "\\", d[0].Value)
}

func TestDetectSyntax(t *testing.T) {
	t.Parallel()

	dt := `# syntax = dockerfile:experimental // opts
FROM busybox
`
	ref, cmdline, loc, ok := DetectSyntax([]byte(dt))
	require.True(t, ok)
	require.Equal(t, "dockerfile:experimental", ref)
	require.Equal(t, "dockerfile:experimental // opts", cmdline)
	require.Equal(t, 1, loc[0].Start.Line)
	require.Equal(t, 1, loc[0].End.Line)

	dt = `#!/bin/sh
# syntax = dockerfile:experimental
FROM busybox
`
	ref, _, loc, ok = DetectSyntax([]byte(dt))
	require.True(t, ok)
	require.Equal(t, "dockerfile:experimental", ref)
	require.Equal(t, 2, loc[0].Start.Line)
	require.Equal(t, 2, loc[0].End.Line)

	dt = `#!/bin/sh

# syntax = dockerfile:experimental
`
	_, _, _, ok = DetectSyntax([]byte(dt))
	require.False(t, ok)

	dt = `FROM busybox
RUN ls
`
	ref, cmdline, _, ok = DetectSyntax([]byte(dt))
	require.False(t, ok)
	require.Equal(t, "", ref)
	require.Equal(t, "", cmdline)

	dt = `//syntax=foo
//key=value`
	ref, _, _, ok = DetectSyntax([]byte(dt))
	require.True(t, ok)
	require.Equal(t, "foo", ref)

	dt = `#!/bin/sh
//syntax=x`
	ref, _, _, ok = DetectSyntax([]byte(dt))
	require.True(t, ok)
	require.Equal(t, "x", ref)

	dt = `{"syntax": "foo"}`
	ref, _, _, ok = DetectSyntax([]byte(dt))
	require.True(t, ok)
	require.Equal(t, "foo", ref)

	dt = `{"syntax": "foo"`
	_, _, _, ok = DetectSyntax([]byte(dt))
	require.False(t, ok)

	dt = `{"syntax": "foo"}
# syntax=bar`
	_, _, _, ok = DetectSyntax([]byte(dt))
	require.False(t, ok)
}

func TestParseDirective(t *testing.T) {
	t.Parallel()

	dt := `#check = skip=all // opts
FROM busybox
`
	ref, cmdline, loc, ok := ParseDirective("check", []byte(dt))
	require.True(t, ok)
	require.Equal(t, "skip=all", ref)
	require.Equal(t, "skip=all // opts", cmdline)
	require.Equal(t, 1, loc[0].Start.Line)
	require.Equal(t, 1, loc[0].End.Line)

	dt = `#!/bin/sh
# check = skip=all
FROM busybox
`
	ref, _, loc, ok = ParseDirective("check", []byte(dt))
	require.True(t, ok)
	require.Equal(t, "skip=all", ref)
	require.Equal(t, 2, loc[0].Start.Line)
	require.Equal(t, 2, loc[0].End.Line)

	dt = `#!/bin/sh

# check = skip=all
`
	_, _, _, ok = ParseDirective("check", []byte(dt))
	require.False(t, ok)

	dt = `FROM busybox
RUN ls
`
	ref, cmdline, _, ok = ParseDirective("check", []byte(dt))
	require.False(t, ok)
	require.Equal(t, "", ref)
	require.Equal(t, "", cmdline)

	dt = `//check=skip=all
//key=value`
	ref, _, _, ok = ParseDirective("check", []byte(dt))
	require.True(t, ok)
	require.Equal(t, "skip=all", ref)

	dt = `#!/bin/sh
//check=skip=all`
	ref, _, _, ok = ParseDirective("check", []byte(dt))
	require.True(t, ok)
	require.Equal(t, "skip=all", ref)

	dt = `{"check": "skip=all"}`
	ref, _, _, ok = ParseDirective("check", []byte(dt))
	require.True(t, ok)
	require.Equal(t, "skip=all", ref)

	dt = `{"check": "foo"`
	_, _, _, ok = ParseDirective("check", []byte(dt))
	require.False(t, ok)

	dt = `{"check": "foo"}
# syntax=bar`
	_, _, _, ok = ParseDirective("check", []byte(dt))
	require.False(t, ok)
}
