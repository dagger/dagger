// Package buildinfo wraps runtime/debug.ReadBuildInfo with optional VCS
// settings injection via -ldflags.
//
// Go's standard `go build` populates VCS settings (vcs.revision, vcs.modified,
// vcs.time) by inspecting the local Git working tree at build time. In some
// build environments — sandboxed builders, CI pipelines that ship source as
// tarballs, vendored snapshots — the working tree is absent and the settings
// come out empty.
//
// This package lets a build wrapper inject those settings via -ldflags,
// mirroring what `go build` would have stamped natively:
//
//	go build -ldflags "
//	    -X github.com/dagger/dagger/internal/version/buildinfo.InjectedVCS=git
//	    -X github.com/dagger/dagger/internal/version/buildinfo.InjectedVCSRevision=$(git rev-parse HEAD)
//	    -X github.com/dagger/dagger/internal/version/buildinfo.InjectedVCSModified=false
//	    -X github.com/dagger/dagger/internal/version/buildinfo.InjectedVCSTime=$(git log -1 --format=%cI)
//	" ./...
//
// At runtime, callers use ReadBuildInfo just like they would runtime/debug's.
// Non-empty Injected* variables override the corresponding settings; empty
// ones leave whatever the toolchain stamped natively untouched.
package buildinfo

import "runtime/debug"

// Set at link time via `go build -ldflags "-X <pkg>.InjectedXxx=..."`. Empty
// means "no override; defer to whatever runtime/debug.ReadBuildInfo returns".
var (
	InjectedVCS         string // VCS system, typically "git"
	InjectedVCSRevision string // commit hash
	InjectedVCSModified string // "true" or "false"
	InjectedVCSTime     string // RFC3339 commit time
)

// ReadBuildInfo returns the build information of the running binary, like
// runtime/debug.ReadBuildInfo, with VCS settings overridden by any non-empty
// Injected* package variables.
//
// If runtime/debug.ReadBuildInfo returns ok=false (binary built outside
// module mode) but any Injected* variable is set, ReadBuildInfo still returns
// a usable *debug.BuildInfo populated with the injected settings, and ok=true.
func ReadBuildInfo() (*debug.BuildInfo, bool) {
	info, ok := debug.ReadBuildInfo()
	overrides := []debug.BuildSetting{
		{Key: "vcs", Value: InjectedVCS},
		{Key: "vcs.revision", Value: InjectedVCSRevision},
		{Key: "vcs.modified", Value: InjectedVCSModified},
		{Key: "vcs.time", Value: InjectedVCSTime},
	}
	var injected bool
	for _, o := range overrides {
		if o.Value == "" {
			continue
		}
		injected = true
		if info == nil {
			info = &debug.BuildInfo{}
		}
		info.Settings = setSetting(info.Settings, o.Key, o.Value)
	}
	return info, ok || injected
}

func setSetting(settings []debug.BuildSetting, key, value string) []debug.BuildSetting {
	for i, s := range settings {
		if s.Key == key {
			settings[i].Value = value
			return settings
		}
	}
	return append(settings, debug.BuildSetting{Key: key, Value: value})
}
