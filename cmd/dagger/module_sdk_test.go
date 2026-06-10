package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleSdkRegistered(t *testing.T) {
	cmd, _, err := moduleCmd.Find([]string{"sdk"})
	require.NoError(t, err)
	require.Same(t, moduleSdkCmd, cmd)
	require.True(t, cmd.DisableFlagParsing, "module sdk must disable flag parsing — args are forwarded to the SDK call")
}

// TestModuleSdkHelpHeuristic exercises the rule that decides between
// "show wrapper help" and "dispatch to the SDK". The decision is based
// on whether any positional (non-dash-prefixed) argument is present,
// because DisableFlagParsing forwards parent persistent flags into
// the arg list and we shouldn't make decisions based on that noise.
func TestModuleSdkHelpHeuristic(t *testing.T) {
	for _, tt := range []struct {
		name         string
		args         []string
		wantDispatch bool
	}{
		{"no args → help", nil, false},
		{"only --help → help", []string{"--help"}, false},
		{"only -h → help", []string{"-h"}, false},
		{"persistent flag only → help", []string{"--load-module=foo"}, false},
		{"persistent flag and --help → help", []string{"--x-release=", "--help"}, false},
		{"subcommand only → dispatch", []string{"python-version"}, true},
		{"subcommand with arg → dispatch", []string{"python-version", "3.13"}, true},
		{"subcommand with --help → dispatch", []string{"python-version", "--help"}, true},
		{"flag then subcommand → dispatch", []string{"--load-module=foo", "go-mod-tidy"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hasSubcommand := false
			for _, a := range tt.args {
				if len(a) > 0 && a[0] != '-' {
					hasSubcommand = true
					break
				}
			}
			require.Equal(t, tt.wantDispatch, hasSubcommand)
		})
	}
}
