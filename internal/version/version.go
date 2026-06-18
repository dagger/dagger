// Package version exposes the current Dagger build's version, commit, and
// dirty state.
//
// The semantic version is the contents of the VERSION file at the package
// root, embedded at compile time via //go:embed.
//
// Commit and dirty state come from runtime/debug build info, wrapped by
// github.com/dagger/go/buildinfo. Native `go build` outside the Dagger
// sandbox gets these for free from the toolchain's VCS detection. Sandboxed
// Dagger builds inject them via -ldflags into buildinfo's Injected* vars.
package version

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/dagger/go/buildinfo"
)

//go:embed VERSION
var raw string

// Version is the semantic version of this Dagger build (e.g. "0.21.3"),
// read from the embedded VERSION file.
var Version string

// Commit is the VCS commit hash this binary was built from, or "" if unknown.
var Commit string

// CommitTime is the commit time as RFC3339, or "" if unknown.
var CommitTime string

// Dirty reports whether the source tree was modified at build time.
var Dirty bool

func init() {
	Version = strings.TrimSpace(raw)

	info, ok := buildinfo.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			Commit = s.Value
		case "vcs.modified":
			Dirty = s.Value == "true"
		case "vcs.time":
			CommitTime = s.Value
		}
	}
}

// Canonical returns the canonical build identifier:
//
//	"0.21.3+42424242"        clean build
//	"0.21.3+42424242.dirty"  dirty build
//	"0.21.3"                 commit unknown
//
// The commit is truncated to 8 characters for readability; use Commit directly
// for the full hash.
func Canonical() string {
	if Commit == "" {
		return Version
	}
	out := fmt.Sprintf("%s+%s", Version, ShortCommit())
	if Dirty {
		out += ".dirty"
	}
	return out
}

// ShortCommit returns the short VCS revision used in human-readable version
// output, or "" if the build's revision is unknown.
func ShortCommit() string {
	if Commit == "" {
		return ""
	}
	if len(Commit) > 8 {
		return Commit[:8]
	}
	return Commit
}

// CommitState returns the short VCS revision, with "+dirty" appended when the
// source tree had modifications at build time.
func CommitState() string {
	if Commit == "" {
		return ""
	}
	out := ShortCommit()
	if Dirty {
		out += "+dirty"
	}
	return out
}
