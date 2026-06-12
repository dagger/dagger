package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/otel-go/oteltestctx"
	"github.com/dagger/testctx"
	"github.com/spf13/cobra"
)

func TestMain(m *testing.M) {
	os.Exit(oteltestctx.Main(m))
}

func Middleware() []testctx.Middleware[*testing.T] {
	return []testctx.Middleware[*testing.T]{
		oteltestctx.WithTracing[*testing.T](
			oteltestctx.TraceConfig[*testing.T]{
				StartOptions: testutil.SpanOpts[*testing.T],
			},
		),
	}
}

func TestRunRootNoArgsShowsUsage(t *testing.T) {
	var out bytes.Buffer
	cmd := &cobra.Command{
		Use:  rootCmd.Use,
		RunE: runRoot,
	}
	cmd.SetUsageTemplate(usageTemplate)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(*cobra.Command, []string) {},
	})

	if err := runRoot(cmd, nil); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	for _, want := range []string{
		"USAGE",
		"dagger [options] [subcommand | file...]",
		"AVAILABLE COMMANDS",
		"version",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage output missing %q:\n%s", want, got)
		}
	}
}

func TestRunRootNoArgsWithLegacyRootShellFlagDispatchesShell(t *testing.T) {
	called := false
	cmd := &cobra.Command{
		Use:  rootCmd.Use,
		RunE: runRoot,
	}
	cmd.Flags().String("command", "", "")
	cmd.SetArgs([]string{"--command", "container"})
	cmd.AddCommand(&cobra.Command{
		Use:    "shell",
		Hidden: true,
		Run: func(*cobra.Command, []string) {
			called = true
		},
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected shell command to run")
	}
}

func TestRunRootNoArgsWithPersistentFlagShowsUsage(t *testing.T) {
	var out bytes.Buffer
	called := false
	cmd := &cobra.Command{
		Use:  rootCmd.Use,
		RunE: runRoot,
	}
	cmd.SetUsageTemplate(usageTemplate)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.PersistentFlags().Bool("debug", false, "")
	cmd.SetArgs([]string{"--debug"})
	cmd.AddCommand(&cobra.Command{
		Use:    "shell",
		Hidden: true,
		Run: func(*cobra.Command, []string) {
			called = true
		},
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("did not expect shell command to run")
	}
	if !strings.Contains(out.String(), "USAGE") {
		t.Fatalf("usage output missing USAGE:\n%s", out.String())
	}
}
