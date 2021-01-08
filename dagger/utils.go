package dagger

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
)

func cueErr(err error) error {
	return fmt.Errorf("%s", cueerrors.Details(err, &cueerrors.Config{}))
}

func debugf(msg string, args ...interface{}) {
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	if os.Getenv("DEBUG") != "" {
		fmt.Fprintf(os.Stderr, msg, args...)
	}
}

func randomID(size int) (string, error) {
	b := make([]byte, size)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func cueStringsToCuePath(parts ...string) cue.Path {
	selectors := make([]cue.Selector, 0, len(parts))
	for _, part := range parts {
		selectors = append(selectors, cue.Str(part))
	}
	return cue.MakePath(selectors...)
}

func cuePathToStrings(p cue.Path) []string {
	selectors := p.Selectors()
	out := make([]string, len(selectors))
	for i, sel := range selectors {
		out[i] = sel.String()
	}
	return out
}
