package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func withTempHintsPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cli-hints.json")
	prev := cliHintsPathFn
	cliHintsPathFn = func() string { return path }
	t.Cleanup(func() { cliHintsPathFn = prev })
	return path
}

func TestReadCliHintsMissingFile(t *testing.T) {
	withTempHintsPath(t)
	state := readCliHints()
	require.NotNil(t, state)
	require.NotNil(t, state.Shown)
	require.Empty(t, state.Shown)
}

func TestReadCliHintsMalformedJSON(t *testing.T) {
	path := withTempHintsPath(t)
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
	state := readCliHints()
	require.NotNil(t, state)
	require.Empty(t, state.Shown)
}

func TestReadCliHintsWrongVersion(t *testing.T) {
	path := withTempHintsPath(t)
	data, err := json.Marshal(map[string]any{
		"version": cliHintsFileVersion + 99,
		"shown": map[string]map[string]string{
			"github.com/dagger/dagger": {"autocheck-after-check": "2026-05-26T00:00:00Z"},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	state := readCliHints()
	require.Empty(t, state.Shown, "wrong version should be treated as empty, not parsed")
}

func TestRecordCliHintRoundTrip(t *testing.T) {
	withTempHintsPath(t)
	state := readCliHints()
	require.False(t, isCliHintShown(state, "github.com/acme/api", "autocheck-after-check"))
	recordCliHintShown(state, "github.com/acme/api", "autocheck-after-check")

	reread := readCliHints()
	require.True(t, isCliHintShown(reread, "github.com/acme/api", "autocheck-after-check"))
}

func TestRecordCliHintPreservesOtherEntries(t *testing.T) {
	withTempHintsPath(t)

	// process A records repo X
	stateA := readCliHints()
	recordCliHintShown(stateA, "github.com/dagger/x", "autocheck-after-check")

	// process B reads (now contains X), then records Y; its in-memory state
	// already has X so this is the easy case
	stateB := readCliHints()
	recordCliHintShown(stateB, "github.com/dagger/y", "autocheck-after-check")

	final := readCliHints()
	require.True(t, isCliHintShown(final, "github.com/dagger/x", "autocheck-after-check"))
	require.True(t, isCliHintShown(final, "github.com/dagger/y", "autocheck-after-check"))
}

func TestRecordCliHintMergesOnConcurrentRace(t *testing.T) {
	withTempHintsPath(t)

	// Both processes read the empty file
	stateA := readCliHints()
	stateB := readCliHints()

	// Process A records for repo X and writes.
	recordCliHintShown(stateA, "github.com/dagger/x", "autocheck-after-check")

	// Process B never saw A's write in its in-memory state, but its write
	// must not erase A's entry because writeCliHints merges with disk.
	recordCliHintShown(stateB, "github.com/dagger/y", "autocheck-after-check")

	final := readCliHints()
	require.True(t, isCliHintShown(final, "github.com/dagger/x", "autocheck-after-check"),
		"A's entry must survive B's later write")
	require.True(t, isCliHintShown(final, "github.com/dagger/y", "autocheck-after-check"))
}

func TestWriteCliHintsFilePermissions(t *testing.T) {
	path := withTempHintsPath(t)
	state := readCliHints()
	recordCliHintShown(state, "github.com/dagger/dagger", "autocheck-after-check")

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"hints file must be 0o600 to avoid leaking repo paths to other users")
}
