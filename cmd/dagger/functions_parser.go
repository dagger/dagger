package main

import (
	"slices"
	"strings"

	"github.com/spf13/pflag"
)

// stripKnownArgs removes all known flags from the given args slice.
//
// It's expected to run this after the flags have been parsed, while ignoring
// unknown flags and arguments.
func stripKnownArgs(args []string, known *pflag.FlagSet) []string {
	p := &argParser{
		known: known,
		args:  make([]string, 0, len(args)),
	}
	return p.parse(args)
}

type argParser struct {
	known *pflag.FlagSet
	args  []string
}

func (p *argParser) parse(args []string) []string {
	for len(args) > 0 {
		s := args[0]
		args = args[1:]

		// positional argument, assumed interspersed
		if !strings.HasPrefix(s, "-") || len(s) < 2 {
			p.args = append(p.args, s)
			continue
		}

		if s[1] == '-' {
			args = p.stripLongFlag(s, args)
		} else {
			// can be a series of shorthand letters of flags (e.g. "-vvv").
			short := s[1:]
			for len(short) > 0 {
				short, args = p.stripShortFlag(short, args)
			}
		}
	}
	return slices.Clip(p.args)
}

func (p *argParser) stripLongFlag(s string, a []string) []string {
	// keep --help
	if s == "--help" {
		p.args = append(p.args, s)
		return a
	}

	split := strings.SplitN(s, "=", 2)
	name := strings.TrimPrefix(split[0], "--")
	flag := p.known.Lookup(name)

	if flag != nil {
		// '--flag=arg' or '--flag' (optional arg)
		if len(split) == 2 || len(a) == 0 || flag.NoOptDefVal != "" {
			return a
		}
		// '--flag arg'
		return a[1:]
	}

	// --unknown or --unknown=uknownval
	p.args = append(p.args, s)

	if len(split) == 2 {
		return p.stripUnknownFlagValue(a)
	}

	return a
}

func (p *argParser) stripUnknownFlagValue(a []string) []string {
	if len(a) > 0 {
		next := a[0]
		// --unknown unknownval
		if len(next) > 0 && next[0] != '-' {
			p.args = append(p.args, next)
			return a[1:]
		}
	}
	return a
}

func (p *argParser) stripShortFlag(s string, a []string) (string, []string) {
	out := s[1:]
	c := string(s[0])

	// keep -h
	if c == "h" {
		p.args = append(p.args, "-h")
		return out, a
	}

	flag := p.known.ShorthandLookup(c)

	// '-f=arg'
	if len(s) > 2 && s[1] == '=' {
		if flag == nil {
			p.args = append(p.args, "-"+s)
		}
		return "", a
	}

	if flag == nil {
		return out, p.stripUnknownFlagValue(a)
	}

	// '-f' (arg is optional)
	if flag.NoOptDefVal != "" {
		return out, a
	}

	// '-farg'
	if len(out) > 1 {
		return "", a
	}

	// '-f arg'
	if len(a) > 0 {
		return out, a[1:]
	}

	return out, a
}
