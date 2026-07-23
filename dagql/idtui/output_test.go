package idtui

import "testing"

func TestRunningInAgentSignals(t *testing.T) {
	for _, name := range agentEnvVars {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{name: "1"}
			if !runningInAgent(func(name string) string { return env[name] }) {
				t.Fatalf("expected %s to indicate an agent", name)
			}
		})
	}
}

func TestRunningInAgentIgnoresUnrelatedAndEmptySignals(t *testing.T) {
	tests := map[string]map[string]string{
		"empty":             {},
		"CI":                {"CI": "true"},
		"empty agent value": {"CODEX_CI": ""},
	}
	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			if runningInAgent(func(name string) string { return env[name] }) {
				t.Fatal("unexpected agent detection")
			}
		})
	}
}
