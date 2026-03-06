package main

import "testing"

func TestDefaultRunServerURLFromEnv(t *testing.T) {
	t.Setenv(odagServerEnvVar, "http://odag.example:5454")
	got := defaultRunServerURL()
	if got != "http://odag.example:5454" {
		t.Fatalf("expected env-derived URL, got %q", got)
	}
}

func TestDefaultRunServerURLFallback(t *testing.T) {
	t.Setenv(odagServerEnvVar, " ")
	got := defaultRunServerURL()
	want := "http://" + defaultListenAddr
	if got != want {
		t.Fatalf("expected fallback URL %q, got %q", want, got)
	}
}

func TestNewRunCmdServerFlagDefaultUsesEnv(t *testing.T) {
	t.Setenv(odagServerEnvVar, "http://odag.env:9999")
	cmd := newRunCmd()
	flag := cmd.Flag("server")
	if flag == nil {
		t.Fatalf("missing server flag")
	}
	if flag.DefValue != "http://odag.env:9999" {
		t.Fatalf("expected default flag value from env, got %q", flag.DefValue)
	}
}

func TestNewServeCmdHasDevFlags(t *testing.T) {
	cmd := newServeCmd()

	devFlag := cmd.Flag("dev")
	if devFlag == nil {
		t.Fatalf("missing dev flag")
	}
	if devFlag.DefValue != "false" {
		t.Fatalf("unexpected dev default: %q", devFlag.DefValue)
	}

	webDirFlag := cmd.Flag("web-dir")
	if webDirFlag == nil {
		t.Fatalf("missing web-dir flag")
	}
	if webDirFlag.DefValue == "" {
		t.Fatalf("expected non-empty web-dir default")
	}
}

func TestNewRootCmdIncludesRebuild(t *testing.T) {
	cmd := newRootCmd()
	rebuild, _, err := cmd.Find([]string{"rebuild"})
	if err != nil {
		t.Fatalf("find rebuild command: %v", err)
	}
	if rebuild == nil || rebuild.Name() != "rebuild" {
		t.Fatalf("expected rebuild command, got %#v", rebuild)
	}
}

func TestNewRebuildCmdHasDBFlag(t *testing.T) {
	cmd := newRebuildCmd()
	dbFlag := cmd.Flag("db")
	if dbFlag == nil {
		t.Fatalf("missing db flag")
	}
	if dbFlag.DefValue != defaultDBPath {
		t.Fatalf("unexpected db default: %q", dbFlag.DefValue)
	}
}
