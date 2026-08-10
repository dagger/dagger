package schema

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

type workspaceReplay struct {
	WorkspaceSequences [][]string `json:"workspace_sequences"`
}

type workspaceReplayState struct {
	Source     string
	Owned      bool
	Changes    int
	Collisions int
}

func TestProperty04WorkspaceInstallationCollisionSafeReversibleReplay(t *testing.T) {
	contents, err := os.ReadFile("../../sdk/rust/completeness/engine-foundation-replay.json")
	require.NoError(t, err)
	var replay workspaceReplay
	require.NoError(t, json.Unmarshal(contents, &replay))
	require.NotEmpty(t, replay.WorkspaceSequences)

	for index := range 256 {
		sequence := replay.WorkspaceSequences[index%len(replay.WorkspaceSequences)]
		repetitions := index%23 + 1
		observed := workspaceReplayState{}
		expected := workspaceReplayState{}
		cfg := &workspace.Config{}
		for offset := range repetitions {
			action := sequence[offset%len(sequence)]
			applyWorkspaceReplayAction(t, cfg, &observed, action)
			referenceWorkspaceReplayAction(&expected, action)
		}
		require.Equal(t, expected, observed, "replay case %d", index)
	}
}

func applyWorkspaceReplayAction(
	t *testing.T,
	cfg *workspace.Config,
	state *workspaceReplayState,
	action string,
) {
	t.Helper()
	const name = "dagger-rust-sdk"
	switch action {
	case "install", "reinstall":
		plan, err := planWorkspaceInstallConfig(cfg, workspaceInstallArgs{
			AsSdk:     true,
			AsSdkName: "rust",
		}, name, "rust")
		if err != nil {
			state.Collisions++
			return
		}
		if plan.Changed {
			state.Changes++
		}
		if plan.Added {
			state.Owned = true
		}
	case "uninstall":
		entry, exists := cfg.Modules[name]
		if state.Owned && exists && entry.Source == "rust" {
			delete(cfg.Modules, name)
			state.Owned = false
			state.Changes++
		}
	case "foreign-install":
		if cfg.Modules == nil {
			cfg.Modules = map[string]workspace.ModuleEntry{}
		}
		if _, exists := cfg.Modules[name]; !exists {
			cfg.Modules[name] = workspace.ModuleEntry{Source: "foreign"}
			state.Changes++
		}
	default:
		t.Fatalf("unknown workspace replay action %q", action)
	}
	if entry, exists := cfg.Modules[name]; exists {
		state.Source = entry.Source
	} else {
		state.Source = ""
	}
}

func referenceWorkspaceReplayAction(state *workspaceReplayState, action string) {
	switch action {
	case "install", "reinstall":
		switch state.Source {
		case "":
			state.Source = "rust"
			state.Owned = true
			state.Changes++
		case "rust":
		default:
			state.Collisions++
		}
	case "uninstall":
		if state.Source == "rust" && state.Owned {
			state.Source = ""
			state.Owned = false
			state.Changes++
		}
	case "foreign-install":
		if state.Source == "" {
			state.Source = "foreign"
			state.Changes++
		}
	}
}
