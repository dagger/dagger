package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineStateCacheKey(t *testing.T) {
	for _, name := range []string{"", "test"} {
		t.Run("isolated/"+name, func(t *testing.T) {
			first := engineStateCacheKey(name, false)
			second := engineStateCacheKey(name, false)
			if first == second {
				t.Fatal("independent engines must not share a state directory")
			}
			if !strings.HasPrefix(first, "dagger-dev-engine-state-") {
				t.Fatalf("unexpected cache key: %q", first)
			}
			if name != "" && !strings.HasSuffix(first, "-"+name) {
				t.Fatalf("cache key %q should retain service name %q", first, name)
			}
		})
	}

	for _, name := range []string{"", "test"} {
		if got := engineStateCacheKey(name, true); got != "dagger-dev-engine-state" {
			t.Fatalf("explicitly shared cache must keep its stable key, got %q", got)
		}
	}
}

func TestIsolatedEngineEntrypoint(t *testing.T) {
	stateDir := t.TempDir()
	entrypoint := filepath.Join(t.TempDir(), "engine entrypoint")
	if err := os.WriteFile(entrypoint, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	// Reuse exactly the same definition and mounted volume, as independent
	// sessions do. A random key chosen while building it cannot isolate these.
	args := append(isolatedEngineEntrypoint(stateDir, entrypoint), "--addr", "tcp://0.0.0.0:1234", "argument with spaces")
	commands := make([]*exec.Cmd, 2)
	outputs := make([]strings.Builder, len(commands))
	for i := range commands {
		commands[i] = exec.Command(args[0], args[1:]...)
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	var roots []string
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("entrypoint: %v: %s", err, outputs[i].String())
		}
		lines := strings.Split(strings.TrimSuffix(outputs[i].String(), "\n"), "\n")
		if len(lines) != 5 || lines[0] != "--root" || lines[2] != "--addr" || lines[3] != "tcp://0.0.0.0:1234" || lines[4] != "argument with spaces" {
			t.Fatalf("unexpected engine arguments: %q", lines)
		}
		root := lines[1]
		if filepath.Dir(root) != stateDir {
			t.Fatalf("engine root %q must stay on volume %q", root, stateDir)
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			t.Fatalf("engine root must exist: %v", err)
		}
		roots = append(roots, root)
	}
	if roots[0] == roots[1] {
		t.Fatal("independent starts of one service definition must not share engine state")
	}
}
