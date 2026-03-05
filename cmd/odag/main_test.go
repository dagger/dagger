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
