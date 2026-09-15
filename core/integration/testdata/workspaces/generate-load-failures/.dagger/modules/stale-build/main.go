package main

// StaleBuild does not compile either, but the regen generator rewrites its directory,
// standing in for a module whose stale generated files this run regenerates.
type StaleBuild struct{}

func (m *StaleBuild) Hello() string {
	return intentionallyUndefinedSymbol()
}
