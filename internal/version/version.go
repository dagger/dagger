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
	"strings"

	"github.com/dagger/go/buildinfo"
)

//go:embed VERSION
var raw string

// version is the bare semantic version of this Dagger build (e.g. "0.21.3"),
// read from the embedded VERSION file with any leading "v" stripped. It is
// unexported so the "v"-prefix choice is always made explicitly at the call
// site, via Version's options.
var version string

// Commit is the VCS commit hash this binary was built from, or "" if unknown.
var Commit string

// CommitTime is the commit time as RFC3339, or "" if unknown.
var CommitTime string

// Dirty reports whether the source tree was modified at build time.
var Dirty bool

func init() {
	version = strings.TrimPrefix(strings.TrimSpace(raw), "v")

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

// Opt configures the string returned by Version.
type Opt func(*options)

type options struct {
	leadingV bool
	commit   bool
}

// WithV prepends a "v" to the version (e.g. "v0.21.3").
func WithV() Opt { return func(o *options) { o.leadingV = true } }

// WithCommit appends the short VCS commit, plus a ".dirty" marker when the
// source tree was modified, when the commit is known (e.g.
// "0.21.3+42424242.dirty"). It is a no-op when the commit is unknown.
func WithCommit() Opt { return func(o *options) { o.commit = true } }

// Version returns this build's semantic version. By default it is bare
// ("0.21.3"); options add the "v" prefix and/or commit provenance:
//
//	Version()                     "0.21.3"
//	Version(WithV())              "v0.21.3"
//	Version(WithCommit())         "0.21.3+42424242"       (or +…​.dirty)
//	Version(WithV(), WithCommit()) "v0.21.3+42424242"
//
// It is the single accessor for the version string; the underlying value is
// unexported so the "v"-prefix choice is always explicit at the call site.
// The commit is truncated to 8 characters; use Commit directly for the full
// hash.
func Version(opts ...Opt) string {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	out := version
	if o.leadingV {
		out = "v" + out
	}
	if o.commit && Commit != "" {
		out += "+" + ShortCommit()
		if Dirty {
			out += ".dirty"
		}
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
