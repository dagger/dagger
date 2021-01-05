package dagger

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	cueformat "cuelang.org/go/cue/format"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/imagemetaresolver"
)

func cuePrint(v cue.Value) (string, error) {
	b, err := cueformat.Node(v.Syntax())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func cueErr(err error) error {
	return fmt.Errorf("%s", cueerrors.Details(err, &cueerrors.Config{}))
}

func debugJSON(v interface{}) {
	if os.Getenv("DEBUG") != "" {
		e := json.NewEncoder(os.Stderr)
		e.SetIndent("", "  ")
		e.Encode(v)
	}
}

func debugf(msg string, args ...interface{}) {
	if !strings.HasSuffix(msg, "\n") {
		msg = msg + "\n"
	}
	if os.Getenv("DEBUG") != "" {
		fmt.Fprintf(os.Stderr, msg, args...)
	}
}

func debug(msg string) {
	if os.Getenv("DEBUG") != "" {
		fmt.Fprintln(os.Stderr, msg)
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

// LLB Helper to pull a Docker image + all its metadata
func llbDockerImage(ref string) llb.State {
	return llb.Image(
		ref,
		llb.WithMetaResolver(imagemetaresolver.Default()),
	)
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

// Validate a cue path, and return a canonical version
func cueCleanPath(p string) (string, error) {
	cp := cue.ParsePath(p)
	return cp.String(), cp.Err()
}
