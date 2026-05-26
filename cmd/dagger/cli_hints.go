package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

const cliHintsFileVersion = 1

type cliHintsState struct {
	Version int                          `json:"version"`
	Shown   map[string]map[string]string `json:"shown"`
}

func cliHintsPath() string {
	return filepath.Join(xdg.StateHome, "dagger", "cli-hints.json")
}

func readCliHints() *cliHintsState {
	state := &cliHintsState{Version: cliHintsFileVersion, Shown: map[string]map[string]string{}}
	data, err := os.ReadFile(cliHintsPath())
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, state); err != nil {
		return &cliHintsState{Version: cliHintsFileVersion, Shown: map[string]map[string]string{}}
	}
	if state.Shown == nil {
		state.Shown = map[string]map[string]string{}
	}
	return state
}

func writeCliHints(state *cliHintsState) {
	path := cliHintsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, "cli-hints.json.*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
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

func cliHintAlreadyShown(repo, hint string) bool {
	state := readCliHints()
	hints, ok := state.Shown[repo]
	if !ok {
		return false
	}
	_, shown := hints[hint]
	return shown
}

func markCliHintShown(repo, hint string) {
	state := readCliHints()
	if state.Shown[repo] == nil {
		state.Shown[repo] = map[string]string{}
	}
	state.Shown[repo][hint] = time.Now().UTC().Format(time.RFC3339)
	writeCliHints(state)
}

// maybeHintAutocheckAfterCheck prints a one-line hint to stderr suggesting
// that the user enable autocheck for the current repo. Best-effort: silent on
// any error, only on stderr TTY, only once per repo.
func maybeHintAutocheckAfterCheck(ctx context.Context) {
	if !stderrIsTTY || silent || quiet > 0 {
		return
	}
	remote, err := gitRemoteOriginURL(ctx)
	if err != nil {
		return
	}
	repo, err := normalizeGitRepo(remote)
	if err != nil {
		return
	}
	const hint = "autocheck-after-check"
	if cliHintAlreadyShown(repo, hint) {
		return
	}
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Tip: run these on every push — 'dagger repo enable autocheck'")
	markCliHintShown(repo, hint)
}
