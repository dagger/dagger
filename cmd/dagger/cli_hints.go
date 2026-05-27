package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

const cliHintsFileVersion = 1

type cliHintsState struct {
	Version int                          `json:"version"`
	Shown   map[string]map[string]string `json:"shown"`
}

// cliHintsPathFn returns the path to the hint state file. Var-of-func so
// tests can override.
var cliHintsPathFn = func() string {
	return filepath.Join(xdg.StateHome, "dagger", "cli-hints.json")
}

func newCliHintsState() *cliHintsState {
	return &cliHintsState{Version: cliHintsFileVersion, Shown: map[string]map[string]string{}}
}

// readCliHints returns the on-disk state, or a fresh empty state if the file
// is missing, unreadable, malformed, or written by a different schema version.
// Never returns an error.
func readCliHints() *cliHintsState {
	data, err := os.ReadFile(cliHintsPathFn())
	if err != nil {
		return newCliHintsState()
	}
	var state cliHintsState
	if err := json.Unmarshal(data, &state); err != nil {
		return newCliHintsState()
	}
	if state.Version != cliHintsFileVersion {
		return newCliHintsState()
	}
	if state.Shown == nil {
		state.Shown = map[string]map[string]string{}
	}
	return &state
}

// writeCliHints persists state. Before writing it re-reads the current
// on-disk state and merges any entries we don't have, so concurrent writers
// for different repos do not silently drop each other's records. All errors
// are swallowed; worst case the hint shows twice.
func writeCliHints(state *cliHintsState) {
	path := cliHintsPathFn()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	mergeWithDisk(state)

	tmp, err := os.CreateTemp(dir, "cli-hints.json.*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
	}
}

// mergeWithDisk additively merges current disk state into the given state,
// so we don't drop entries we never knew about. In-memory state wins on
// conflicts (we just wrote them).
func mergeWithDisk(state *cliHintsState) {
	disk := readCliHints()
	for repo, hints := range disk.Shown {
		if state.Shown[repo] == nil {
			state.Shown[repo] = map[string]string{}
		}
		for hint, ts := range hints {
			if _, exists := state.Shown[repo][hint]; !exists {
				state.Shown[repo][hint] = ts
			}
		}
	}
}

func isCliHintShown(state *cliHintsState, repo, hint string) bool {
	hints, ok := state.Shown[repo]
	if !ok {
		return false
	}
	_, shown := hints[hint]
	return shown
}

func recordCliHintShown(state *cliHintsState, repo, hint string) {
	if state.Shown[repo] == nil {
		state.Shown[repo] = map[string]string{}
	}
	state.Shown[repo][hint] = time.Now().UTC().Format(time.RFC3339)
	writeCliHints(state)
}

// maybeHintAutocheckAfterCheck prints a one-line hint to stderr suggesting
// that the user enable autocheck for the current repo. Best-effort: silent
// on any error, only on stderr TTY, only once per canonical repo.
func maybeHintAutocheckAfterCheck(ctx context.Context) {
	if !stderrIsTTY || silent || quiet > 0 {
		return
	}
	if os.Getenv("DAGGER_NO_HINTS") != "" {
		return
	}
	// If stderr has been redirected to a log wrapper, skip — output is not
	// going to a human terminal even if the underlying fd is one.
	if os.Getenv("DAGGER_LOG_STDERR") != "" {
		return
	}
	remote, err := gitRemoteOriginURLForHint(ctx)
	if err != nil {
		return
	}
	repo, err := normalizeGitRepo(remote)
	if err != nil {
		return
	}
	const hint = "autocheck-after-check"
	state := readCliHints()
	if isCliHintShown(state, repo, hint) {
		return
	}
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Tip: run these on every push — 'dagger repo enable autocheck'")
	recordCliHintShown(state, repo, hint)
}

// gitRemoteOriginURLForHint reads remote.origin.url with a short timeout so a
// slow/hung credential helper or filesystem can never delay the hint path.
func gitRemoteOriginURLForHint(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
