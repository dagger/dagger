package daggercmd

import (
	"testing"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceExecCommand(t *testing.T) {
	for _, command := range []string{"workspace", "ws"} {
		cmd, _, err := rootCmd.Find([]string{command, "exec"})
		require.NoError(t, err)
		require.Same(t, workspaceExecCmd, cmd)
	}

	cmd := newWorkspaceExecCmd()
	require.Equal(t, distconsts.AlpineImage, cmd.Flags().Lookup("from").DefValue)
	for _, name := range []string{"container", "image", "base", "scope"} {
		require.Nil(t, cmd.Flags().Lookup(name), name)
	}
	require.NotNil(t, cmd.Flags().Lookup("include"))
	require.NotNil(t, cmd.Flags().Lookup("exclude"))
	require.NotNil(t, cmd.Flags().Lookup("no-apply"))
	require.NoError(t, validateFlagCapabilities(testRootCommand(), []string{"workspace", "exec", "--no-apply", "echo", "ok"}))
	require.NoError(t, validateFlagCapabilities(testRootCommand(), []string{"workspace", "exec", "--auto-apply", "echo", "ok"}))
}

func TestWorkspaceExecArgumentParsing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "optional separator",
			args: []string{"--from=alpine:latest", "--", "printf", "%s\\n", "hello"},
			want: []string{"printf", "%s\\n", "hello"},
		},
		{
			name: "command flags without separator",
			args: []string{"printf", "%s\\n", "-w"},
			want: []string{"printf", "%s\\n", "-w"},
		},
		{
			name: "recognized CLI flag after command",
			args: []string{"echo", "--from=busybox"},
			want: []string{"echo", "--from=busybox"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newWorkspaceExecCmd()
			var got []string
			// Capture parsed arguments through Args, which runs after flag parsing
			// and before RunE.
			cmd.Args = func(_ *cobra.Command, args []string) error {
				got = append([]string{}, args...)
				return nil
			}
			cmd.RunE = func(_ *cobra.Command, _ []string) error { return nil }
			cmd.SetArgs(tc.args)
			require.NoError(t, cmd.Execute())
			require.Equal(t, tc.want, got)
		})
	}

	cmd := newWorkspaceExecCmd()
	cmd.RunE = func(_ *cobra.Command, _ []string) error { return nil }
	cmd.SetArgs([]string{"--from=myregistry.example/team/tool", "true"})
	require.NoError(t, cmd.Execute())
	require.Equal(t, "myregistry.example/team/tool", cmd.Flags().Lookup("from").Value.String())
}

func TestWorkspaceExecDisposition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		apply   bool
		noApply bool
		want    changesetDisposition
		wantErr string
	}{
		{name: "prompt", want: changesetDispositionPrompt},
		{name: "apply", apply: true, want: changesetDispositionApply},
		{name: "no apply", noApply: true, want: changesetDispositionNoApply},
		{name: "conflict", apply: true, noApply: true, want: changesetDispositionPrompt, wantErr: "cannot be used together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workspaceExecDisposition(tc.apply, tc.noApply)
			require.Equal(t, tc.want, got)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

func TestWorkspaceExecWorkdir(t *testing.T) {
	for _, tc := range []struct {
		cwd     string
		want    string
		wantErr bool
	}{
		{cwd: "", want: "/ws"},
		{cwd: "items/deep", want: "/ws/items/deep"},
		{cwd: "../outside", wantErr: true},
		{cwd: "/outside", wantErr: true},
	} {
		got, err := workspaceExecWorkdir(tc.cwd)
		if tc.wantErr {
			require.Error(t, err, tc.cwd)
		} else {
			require.NoError(t, err, tc.cwd)
			require.Equal(t, tc.want, got, tc.cwd)
		}
	}
}
